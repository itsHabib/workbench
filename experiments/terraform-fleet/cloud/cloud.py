"""Local controller for this one disposable GCP demo. Never forwards model auth."""
import argparse
import ipaddress
import json
import os
from pathlib import Path
import subprocess
import time
import urllib.request

HERE = Path(__file__).resolve().parent
STATE = HERE / ".runtime"
PROJECT = "rooms-lab-20260914"
ZONE = "us-east1-b"
ROOMS_SOURCE = Path("/Users/mh/dev/rooms")
ROOMS_REVISION = "9512b2a50089a31d000b38e3ae097973f88f693d"


def run(args, **kwargs):
    return subprocess.run([str(a) for a in args], check=True, **kwargs)


def config():
    return json.loads((STATE / "inputs.json").read_text())


def initialize():
    STATE.mkdir(mode=0o700, exist_ok=True)
    key = STATE / "ssh-key"
    if not key.exists():
        run(["ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "tf-fleet-rooms-demo", "-f", key])
    if not (STATE / "inputs.json").exists():
        with urllib.request.urlopen("https://api.ipify.org", timeout=15) as response:
            address = str(ipaddress.IPv4Address(response.read().decode().strip()))
        (STATE / "inputs.json").write_text(json.dumps({"operator_cidr": address + "/32", "ssh_public_key": key.with_suffix(".pub").read_text().strip()}, indent=2) + "\n")


def terraform(args):
    initialize()
    token = subprocess.check_output(["gcloud", "auth", "print-access-token"], text=True).strip()
    env = {**os.environ, "GOOGLE_OAUTH_ACCESS_TOKEN": token, "TF_IN_AUTOMATION": "1"}
    env.update({"TF_VAR_" + k: v for k, v in config().items()})
    return subprocess.run(["terraform", *args], cwd=HERE, env=env).returncode


def host():
    if os.environ.get("ROOMS_HOST"):
        return os.environ["ROOMS_HOST"]
    return subprocess.check_output(["terraform", "output", "-raw", "host"], cwd=HERE, text=True).strip()


def ssh_args():
    return ["ssh", "-i", str(STATE / "ssh-key"), "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes",
            "-o", "StrictHostKeyChecking=accept-new", "-o", "UserKnownHostsFile=" + str(STATE / "known_hosts"),
            "-o", "ConnectTimeout=10", "rooms@" + host()]


def bootstrap():
    # Terraform can supply the address before root-module outputs enter state.
    peer = ssh_args()
    for attempt in range(40):
        if subprocess.run([*peer, "true"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0:
            break
        time.sleep(3)
    else:
        raise RuntimeError("SSH readiness timed out")
    archive = STATE / "rooms-source.tar"
    run(["git", "-C", ROOMS_SOURCE, "archive", "--format=tar", "--output=" + str(archive), ROOMS_REVISION])
    run([*peer, "mkdir -p /home/rooms/src /home/rooms/lab"])
    with archive.open("rb") as source:
        run([*peer, "tar -xf - -C /home/rooms/src"], stdin=source)
    # Rooms' repository tests exercise Git publication, so an archive alone is
    # insufficient. Supply the exact public main history as well as its files.
    main_head = subprocess.check_output(["git", "-C", str(ROOMS_SOURCE), "rev-parse", "main"], text=True).strip()
    if main_head != ROOMS_REVISION:
        raise RuntimeError("Rooms main moved; freeze a new reviewed source explicitly")
    bundle = STATE / "rooms-source.bundle"
    run(["git", "-C", ROOMS_SOURCE, "bundle", "create", bundle, "main"])
    with bundle.open("rb") as source:
        run([*peer, "tee /home/rooms/rooms-source.bundle"], stdin=source, stdout=subprocess.DEVNULL)
    run([*peer, "git -C /home/rooms/src init -q && git -C /home/rooms/src fetch -q /home/rooms/rooms-source.bundle main && git -C /home/rooms/src reset --mixed " + ROOMS_REVISION + " && git -C /home/rooms/src diff --exit-code"])
    with (HERE / "bootstrap.sh").open("rb") as script, (STATE / "bootstrap.log").open("w") as log:
        run([*peer, "bash -s"], stdin=script, stdout=log, stderr=subprocess.STDOUT)
    print("Rooms host prepared; see cloud/.runtime/bootstrap.log", flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("tf", "ssh", "bootstrap"))
    parser.add_argument("args", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.action == "tf":
        raise SystemExit(terraform(args.args))
    if args.action == "bootstrap":
        bootstrap()
        return
    raise SystemExit(subprocess.run([*ssh_args(), *args.args]).returncode)


if __name__ == "__main__":
    main()
