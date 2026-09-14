"""Run the frozen task patch on an already prepared Linux Rooms host."""
import argparse
import base64
import hashlib
import json
from pathlib import Path
import subprocess
import time

BASE = "92a706a7982a527ade967e43b68afd4bc1e5d667"


def sha(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ("rooms", "image", "toolstore", "patch", "out"):
        parser.add_argument("--" + name, type=Path, required=True)
    args = parser.parse_args()
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


if __name__ == "__main__":
    raise SystemExit(main())
