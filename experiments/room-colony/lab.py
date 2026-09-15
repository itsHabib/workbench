"""Plan/apply a two-Room experiment from live processes and durable results.

Mac/Lima transport is the first backend. No daemon decides workflow progression:
author/verifier do that in guests. Re-run apply after the injected interruption.
"""
import argparse
import base64
import fcntl
import hashlib
import io
import json
import os
from pathlib import Path
import secrets
import shlex
import signal
import subprocess
import sys
import tarfile
import time

from broker import write
from workload import digest

HERE = Path(__file__).resolve().parent
ROLES = ("author", "verifier")


def read(path):
    return json.loads(path.read_text())


def alive(pid, marker):
    result = subprocess.run(["ps", "-p", str(pid), "-o", "command="], capture_output=True, text=True)
    return result.returncode == 0 and marker in result.stdout


def source_hash():
    value = b"".join((HERE / name).read_bytes() for name in ("worker.py", "workload.py", "broker.py", "proxy.py", "lab.py"))
    return hashlib.sha256(value).hexdigest()


def settled(attempt):
    terminal, lifecycle = attempt / "terminal.json", attempt / "collected/lifecycle.ndjson"
    if not terminal.exists() or not lifecycle.exists():
        return False
    events = [json.loads(line).get("event") for line in lifecycle.read_text().splitlines()]
    return read(terminal).get("collection_exit") == 0 and all(
        event in events for event in ("collection_done", "cleanup_done"))


def require_collected(root):
    if any(not settled(a) for a in (root / "attempts").iterdir()):
        raise ValueError("preserve remote attempts: local collection/cleanup evidence is incomplete")


def verify_backend(root):
    desired, expected = read(root / "desired.json"), read(root / "backend.json")
    hashes = subprocess.check_output(["limactl", "shell", desired["lima"], "sudo", "sha256sum",
                                      *expected["resources"]], text=True)
    if [line.split()[0] for line in hashes.splitlines()] != expected["sha256"]:
        raise ValueError("backend changed; preserve this run and initialize a fresh experiment")


def plan(root):
    desired = read(root / "desired.json")
    if desired["source_sha256"] != source_hash():
        raise ValueError("source changed; create a fresh experiment instead of mutating a running one")
    broker = read(root / "broker.json")
    if not alive(broker["pid"], str(root)):
        raise ValueError("broker is not running; retain state and restart it before apply")
    actions = []
    for role in ROLES:
        attempts = sorted((root / "attempts").glob(role + "-*"))
        live = [a for a in attempts if (a / "process.json").exists()
                and alive(read(a / "process.json")["pid"], str(a))]
        if live:
            actions.append({"role": role, "action": "keep", "reason": "owned process still running"})
            continue
        if any(not settled(a) for a in attempts):
            actions.append({"role": role, "action": "blocked", "reason": "missing collection/cleanup evidence; inspect Rooms before replacement"})
            continue
        if (root / "artifacts" / (role + "-done.json")).exists():
            successful = any((a / "terminal.json").exists() and read(a / "terminal.json")["exit"] == 0 for a in attempts)
            actions.append({"role": role, "action": "complete" if successful else "blocked",
                            "reason": "durable completion and process result" if successful else "completion lacks clean lifecycle"})
            continue
        actions.append({"role": role, "action": "start" if len(attempts) < 3 else "blocked",
                        "attempt": len(attempts) + 1, "reason": "no live worker or completion"})
    return actions


def init(args):
    root = args.root.resolve()
    root.mkdir(mode=0o700)
    for name in ("artifacts", "model", "attempts"):
        (root / name).mkdir()
    (root / "token").write_text(secrets.token_hex(24))
    desired = {"source_sha256": source_hash(), "roles": list(ROLES), "lima": args.lima,
               "rooms": args.rooms, "image": args.image, "toolstore": args.toolstore,
               "guest_root": "/tmp/colony-" + secrets.token_hex(4), "rounds": 2}
    write(root / "desired.json", desired)
    peer = ["limactl", "shell", args.lima, "sudo"]
    resources = [args.rooms, args.image, args.toolstore.rstrip("/") + "/toolstore.sqfs"]
    hashes = subprocess.check_output([*peer, "sha256sum", *resources], text=True)
    write(root / "backend.json", {"sha256": [line.split()[0] for line in hashes.splitlines()],
                                  "resources": resources})
    home = desired["guest_root"] + "/h"
    # Copy only the room login key, never model/cloud credentials.
    stage = f'mkdir -m 700 {desired["guest_root"]}; mkdir -p {home}/.ssh; install -m 600 "$(getent passwd "$SUDO_USER" | cut -d: -f6)/.ssh/id_rooms" {home}/.ssh/id_rooms'
    subprocess.run([*peer, "sh", "-c", stage], check=True)
    with (root / "broker.log").open("w") as log:
        subprocess.Popen([sys.executable, str(HERE / "broker.py"), str(root)],
                         stdout=log, stderr=log, start_new_session=True, stdin=subprocess.DEVNULL)
    for _ in range(100):
        if (root / "broker.json").exists():
            start_proxy(root, desired)
            return
        time.sleep(.1)
    raise TimeoutError("broker startup")


def start_proxy(root, desired):
    peer = ["limactl", "shell", desired["lima"], "sudo"]
    remote = desired["guest_root"]
    port = read(root / "broker.json")["port"]
    subprocess.run([*peer, "tee", remote + "/proxy.py"], input=(HERE / "proxy.py").read_bytes(),
                   stdout=subprocess.DEVNULL, check=True)
    command = f"nohup python3 {remote}/proxy.py {port} http://192.168.5.2:{port} </dev/null >{remote}/proxy.log 2>&1 & echo $! >{remote}/proxy.pid"
    subprocess.run([*peer, "sh", "-c", command], check=True)


def payload():
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
        for name in ("worker.py", "workload.py"):
            archive.add(HERE / name, arcname=name)
    return base64.b64encode(buffer.getvalue()).decode()


def apply(root):
    with (root / "apply.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        actions = plan(root)
        if any(a["action"] == "start" for a in actions):
            verify_backend(root)
        for action in actions:
            if action["action"] != "start":
                continue
            attempt = root / "attempts" / f'{action["role"]}-{action["attempt"]}'
            attempt.mkdir()
            with (attempt / "wrapper.log").open("w") as log:
                process = subprocess.Popen([sys.executable, str(HERE / "lab.py"), "attempt", str(root),
                                            "--role", action["role"], "--attempt", str(attempt)],
                                           stdout=log, stderr=log, stdin=subprocess.DEVNULL, start_new_session=True)
            write(attempt / "process.json", {"pid": process.pid})
        return actions


def attempt_run(args):
    root, attempt = args.root, args.attempt
    desired, broker = read(root / "desired.json"), read(root / "broker.json")
    remote = desired["guest_root"] + "/" + attempt.name
    subprocess.run(["limactl", "shell", desired["lima"], "sudo", "mkdir", "-p", remote], check=True)
    gateway = "ip route | awk '$1 == \"default\" {print $3; exit}'"
    command = ("set -eu; mkdir -p /tmp/colony; printf %s " + shlex.quote(payload())
               + " | base64 -d | tar -xz -C /tmp/colony; cd /tmp/colony; exec python3 worker.py "
               + args.role + f' "http://$({gateway}):{broker["port"]}" '
               + shlex.quote((root / "token").read_text().strip()))
    argv = ["limactl", "shell", desired["lima"], "sudo", "env", "HOME=" + desired["guest_root"] + "/h",
            desired["rooms"], "run", "--image", desired["image"], "--toolstore", desired["toolstore"],
            "--cpus", "1", "--memory", "512", "--disk", "1", "--command", command,
            "--max-wall", "600s", "--out", remote + "/out", "--lifecycle", remote + "/lifecycle.ndjson", "--json"]
    started = time.monotonic()
    with (attempt / "stdout.txt").open("w") as stdout, (attempt / "stderr.txt").open("w") as stderr:
        result = subprocess.run(argv, stdout=stdout, stderr=stderr)
    peer = ["limactl", "shell", desired["lima"], "sudo"]
    collected = subprocess.run([*peer, "tar", "-C", remote, "-cf", "-", "."], capture_output=True)
    if collected.returncode == 0:
        with tarfile.open(fileobj=io.BytesIO(collected.stdout)) as archive:
            archive.extractall(attempt / "collected", filter="data")
    write(attempt / "terminal.json", {"exit": result.returncode, "collection_exit": collected.returncode,
                                       "elapsed_seconds": time.monotonic() - started})


def audit(root):
    store = root / "artifacts"
    promotion = read(store / "promotion.json") if (store / "promotion.json").exists() else {}
    qualification = read(store / "qualification.json") if (store / "qualification.json").exists() else {}
    selected = read(store / "selected.json") if (store / "selected.json").exists() else {}
    attempts, boots = [], set()
    for attempt in sorted((root / "attempts").iterdir()):
        terminal = read(attempt / "terminal.json") if (attempt / "terminal.json").exists() else {}
        lifecycle = attempt / "collected/lifecycle.ndjson"
        events = [json.loads(line) for line in lifecycle.read_text().splitlines()] if lifecycle.exists() else []
        logs = list((attempt / "collected").rglob("stdout*"))
        for log in logs:
            for line in log.read_text(errors="replace").splitlines():
                try:
                    identity = json.loads(line).get("guest_identity")
                    if identity:
                        boots.add(identity["boot_id"])
                except (ValueError, AttributeError):
                    pass
        attempts.append({"attempt": attempt.name, "terminal": terminal,
                         "events": [e.get("event") or e.get("kind") for e in events]})
    checks = {
        "roles_complete": all((store / (role + "-done.json")).exists() and any(
            a["attempt"].startswith(role + "-") and a["terminal"].get("exit") == 0 for a in attempts) for role in ROLES),
        "two_model_calls": len(list((root / "model").glob("*/reply.json"))) == 2,
        "two_model_requests": len(list((root / "model").glob("*/request.json"))) == 2,
        "interruption_recorded": (store / "interruption.json").exists(),
        "author_replaced": len(list((root / "attempts").glob("author-*"))) == 2,
        "author_killed": any(a["attempt"] == "author-1" and a["terminal"].get("exit") == 137 for a in attempts),
        "distinct_guest_boots": len(boots) == 3,
        "negative_control_rejected": (store / "negative-control.json").exists() and not read(store / "negative-control.json")["valid"],
        "qualification_bound": promotion.get("sha256") == qualification.get("sha256") == selected.get("sha256") and bool(selected),
        "source_digest_valid": bool(selected) and digest(selected.get("source", "")) == selected.get("sha256"),
        "qualification_receipt_valid": bool(qualification) and digest(json.dumps(qualification, sort_keys=True)) == promotion.get("qualification_sha256"),
        "promoted_improvement": promotion.get("promoted") is True and qualification.get("qualified") is True,
        "collected_and_cleaned": bool(attempts) and all(a["terminal"].get("collection_exit") == 0
            and "cleanup_done" in a["events"] and "collection_done" in a["events"] for a in attempts),
    }
    result = {"checks": checks, "passed": all(checks.values()), "attempts": attempts,
              "guest_boot_ids": sorted(boots), "qualification": qualification,
              "run_source_sha256": read(root / "desired.json")["source_sha256"],
              "current_source_matches": read(root / "desired.json")["source_sha256"] == source_hash()}
    write(root / "audit.json", result)
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("verb", choices=["init", "plan", "apply", "attempt", "audit", "stop"])
    parser.add_argument("root", type=Path)
    parser.add_argument("--lima", default="rooms-host")
    parser.add_argument("--rooms", default="/home/mh.guest/rooms-readiness-src/target/release/rooms")
    parser.add_argument("--image", default="/home/mh.guest/rooms/readiness-observation/agent.ext4")
    parser.add_argument("--toolstore", default="/home/mh.guest/rooms/toolchain-lab/python")
    parser.add_argument("--role", choices=ROLES)
    parser.add_argument("--attempt", type=Path)
    args = parser.parse_args()
    args.root = args.root.resolve()
    if args.verb == "init":
        init(args)
    elif args.verb == "attempt":
        attempt_run(args)
    elif args.verb == "plan":
        print(json.dumps(plan(args.root), indent=2))
    elif args.verb == "apply":
        print(json.dumps(apply(args.root), indent=2))
    elif args.verb == "audit":
        result = audit(args.root)
        print(json.dumps(result, indent=2))
        return 0 if result["passed"] else 1
    elif args.verb == "stop":
        # Rooms owns teardown. Do not terminate a wrapper while it is collecting.
        processes = list((args.root / "attempts").glob("*/process.json"))
        if any(alive(read(p)["pid"], str(p.parent)) for p in processes):
            raise ValueError("workers still running; wait for their bounded Rooms lifecycle before stopping broker")
        require_collected(args.root)
        broker = read(args.root / "broker.json")
        if alive(broker["pid"], str(args.root)):
            os.kill(broker["pid"], signal.SIGTERM)
        desired = read(args.root / "desired.json")
        peer = ["limactl", "shell", desired["lima"], "sudo"]
        remote = desired["guest_root"]
        cleanup_proxy = """import os, pathlib, signal, sys
r = pathlib.Path(sys.argv[1]); f = r / 'proxy.pid'
if f.exists():
 p = int(f.read_text()); cmd = pathlib.Path('/proc') / str(p) / 'cmdline'
 if cmd.exists() and str(r / 'proxy.py').encode() in cmd.read_bytes().split(b'\\0'):
  os.kill(p, signal.SIGTERM)
"""
        subprocess.run([*peer, "python3", "-c", cleanup_proxy, remote], check=True)
        inventory = subprocess.check_output([*peer, "env", "HOME=" + desired["guest_root"] + "/h", desired["rooms"], "ls", "--json"])
        (args.root / "final-rooms.json").write_bytes(inventory)
        if json.loads(inventory).get("rooms"):
            raise ValueError("Rooms inventory is not empty; preserve guest state")
        subprocess.run([*peer, "rm", "-rf", "--one-file-system", desired["guest_root"]], check=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
