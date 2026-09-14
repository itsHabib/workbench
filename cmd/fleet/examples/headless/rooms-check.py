"""Run the frozen task patch in a cold room on an already prepared Linux Rooms host.

With --lima HOST it runs from macOS: this same file executes inside the local
Lima VM, and its attempt directory is copied back. It never starts the VM.
"""
import argparse
import base64
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tarfile
import time
import uuid

BASE = "92a706a7982a527ade967e43b68afd4bc1e5d667"


def sha(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("rooms", "image", "toolstore", "patch", "out"):
        parser.add_argument("--" + name, type=Path, required=True)
    parser.add_argument("--lima", help="run inside this local Lima host; rooms/image/toolstore are its paths")
    args = parser.parse_args()
    if args.lima:
        return through_lima(args)
    output = args.out.resolve()
    output.mkdir()  # Never reuse a Rooms lifecycle/output path.
    payload = base64.b64encode(args.patch.read_bytes()).decode("ascii")
    command = ("set -eu; printf '%s' " + payload + " | base64 -d > /tmp/author.patch; "
               "git apply --check /tmp/author.patch; git apply /tmp/author.patch; "
               "PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover "
               "-s cmd/fleet/examples/headless/task -p 'test_*.py'")
    argv = [str(args.rooms.resolve()), "run", "--image", str(args.image.resolve()),
            "--toolstore", str(args.toolstore.resolve()), "--cpus", "2", "--memory", "1024", "--disk", "1",
            "--repo", "https://github.com/itsHabib/workbench", "--base-sha", BASE,
            "--command", command, "--max-wall", "120s", "--out", str(output / "out"),
            "--lifecycle", str(output / "lifecycle.ndjson"), "--json"]
    artifacts = (args.rooms, args.image, args.toolstore / "toolstore.sqfs", args.toolstore / "meta.json", args.patch)
    inputs = {"base": BASE, "argv": argv, "sha256": {str(p.resolve()): sha(p) for p in artifacts}}
    (output / "inputs.json").write_text(json.dumps(inputs, indent=2) + "\n")
    started = time.monotonic()
    # Rooms owns its wall bound and foreground cancellation. Do not kill it before collection.
    with (output / "stdout.txt").open("w") as stdout, (output / "stderr.txt").open("w") as stderr:
        result = subprocess.run(argv, stdout=stdout, stderr=stderr)
    returned = output / "out/result.patch"
    summary = {"cli_exit": result.returncode, "elapsed_seconds": time.monotonic() - started,
               "returned_patch_sha256": sha(returned) if returned.exists() else None,
               "input_patch_sha256": inputs["sha256"][str(args.patch.resolve())],
               "scope": "command, collection and cleanup must be read separately from result.json and lifecycle.ndjson"}
    try:
        command_result = json.loads((output / "out/result.json").read_text())
        summary["command_status"] = command_result.get("status")
        summary["command_exit"] = command_result.get("exit_code")
    except (OSError, ValueError) as error:
        summary["command_status"] = "unknown"
        summary["result_error"] = str(error)
    (output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    print(json.dumps(summary, indent=2))
    return result.returncode


def through_lima(args):
    """Transport only: stage the patch, run this file in the VM, copy the attempt back."""
    output = args.out.resolve()
    output.mkdir()  # Never reuse a local attempt directory either.
    guest = "/tmp/rooms-check-" + uuid.uuid4().hex[:12]
    shell = ["limactl", "shell", args.lima, "sudo"]
    started = time.monotonic()
    subprocess.run([*shell, "mkdir", guest], check=True)
    subprocess.run([*shell, "tee", guest + "/input.patch"], input=args.patch.read_bytes(), stdout=subprocess.DEVNULL, check=True)
    remote = [*shell, "env", "HOME=/tmp/rooms-check-home", "python3", "-", "--rooms", str(args.rooms), "--image", str(args.image),
              "--toolstore", str(args.toolstore), "--patch", guest + "/input.patch", "--out", guest + "/attempt"]
    code = subprocess.run(remote, input=Path(__file__).read_bytes(), stdout=subprocess.DEVNULL).returncode
    archive = subprocess.run([*shell, "tar", "-C", guest + "/attempt", "-cf", "-", "."], capture_output=True, check=True).stdout
    with tarfile.open(fileobj=io.BytesIO(archive)) as collected:
        collected.extractall(output, filter="data")
    subprocess.run([*shell, "rm", "-rf", guest], check=True)
    transport = {"lima_host": args.lima, "guest_attempt": guest + "/attempt", "exit": code,
                 "local_patch_sha256": sha(args.patch), "elapsed_seconds": time.monotonic() - started,
                 "scope": "includes VM transport; the guest copy was removed after collection"}
    (output / "transport.json").write_text(json.dumps(transport, indent=2) + "\n")
    print((output / "summary.json").read_text() if (output / "summary.json").exists() else json.dumps(transport, indent=2))
    return code


if __name__ == "__main__":
    raise SystemExit(main())
