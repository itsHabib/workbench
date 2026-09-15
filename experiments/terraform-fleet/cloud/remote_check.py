"""Run one frozen patch through the existing Rooms adapter on the demo VM."""
import argparse
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tarfile
import time
import uuid


def run(peer, command, **kwargs):
    return subprocess.run([*peer, command], check=True, **kwargs)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--target", type=Path, required=True)
    parser.add_argument("--patch", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    target = json.loads(args.target.read_text())
    peer = target["ssh"]
    output = args.out.resolve()
    output.mkdir()
    attempt = "/home/rooms/lab/fleet-" + uuid.uuid4().hex[:12]
    started = time.time()
    run(peer, "mkdir -m 700 " + attempt)
    run(peer, "tee " + attempt + "/input.patch", input=args.patch.read_bytes(), stdout=subprocess.DEVNULL)
    run(peer, "tee " + attempt + "/check.py", input=Path(target["adapter"]).read_bytes(), stdout=subprocess.DEVNULL)
    command = ("sudo -H python3 " + attempt + "/check.py --rooms /home/rooms/src/target/release/rooms"
               " --image /home/rooms/rooms/images/agent.ext4 --toolstore /home/rooms/lab/python"
               " --patch " + attempt + "/input.patch --out " + attempt + "/result")
    result = subprocess.run([*peer, command], capture_output=True)
    (output / "ssh.stdout").write_bytes(result.stdout)
    (output / "ssh.stderr").write_bytes(result.stderr)
    transport = {"host": target["host"], "remote_attempt": attempt, "ssh_exit": result.returncode,
                 "elapsed_seconds": time.time() - started,
                 "local_patch_sha256": hashlib.sha256(args.patch.read_bytes()).hexdigest()}
    (output / "transport.json").write_text(json.dumps(transport, indent=2) + "\n")
    archive = run(peer, "sudo tar -C " + attempt + "/result -cf - .", capture_output=True).stdout
    with tarfile.open(fileobj=io.BytesIO(archive)) as bundle:
        bundle.extractall(output, filter="data")
    print((output / "summary.json").read_text())
    raise SystemExit(result.returncode)


if __name__ == "__main__":
    main()
