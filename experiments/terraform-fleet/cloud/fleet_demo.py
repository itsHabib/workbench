"""Prepare/run a fresh Fleet, retaining the original failed local trial unchanged."""
import argparse
import hashlib
import importlib
import json
import os
from pathlib import Path
import subprocess
import sys
import shutil

import cloud

sys.dont_write_bytecode = True
SOURCE = Path("/Users/mh/dev/workbench/.claude/worktrees/codex/headless-workbench-poc")
REVISION = "3cea02ad796cd90eb772e57c9560249d0c34984e"
EXAMPLE = SOURCE / "cmd/fleet/examples/headless"
ROOT = Path("/Users/mh/dev/.codex-investigations/fleet-terraform-gcp-v2")


def lab_module():
    head = subprocess.check_output(["git", "-C", str(SOURCE), "rev-parse", "HEAD"], text=True).strip()
    dirty = subprocess.check_output(["git", "-C", str(SOURCE), "status", "--porcelain"], text=True).strip()
    if head != REVISION or dirty:
        raise RuntimeError("Fleet source must be clean and pinned")
    sys.path.insert(0, str(EXAMPLE))
    return importlib.import_module("lab")


def prepare(lab):
    if ROOT.exists():
        raise RuntimeError("fresh demo requires a new lab; existing evidence is never overwritten")
    lab.prepare(str(ROOT), None, str(SOURCE), "codex")
    # A separate Git administration directory is the same layout principle as
    # Fleet's pooled worktrees. Standard git and the audit follow its .git file.
    # Configure BEFORE workers start; do not retrofit the earlier failed run.
    subprocess.run(["git", "init", "--separate-git-dir", str(ROOT / "verifier-admin.git"),
                    str(ROOT / "workbench-verifier")], check=True)
    resolved = ROOT / "resolved.json"
    info = json.loads(resolved.read_text())
    peer = cloud.ssh_args()
    peer[peer.index("StrictHostKeyChecking=accept-new")] = "StrictHostKeyChecking=yes"
    guest_adapter = ROOT / "bin/rooms-guest.py"
    shutil.copy2(EXAMPLE / "rooms-check.py", guest_adapter)
    target = {"host": cloud.host(), "ssh": peer, "adapter": str(guest_adapter)}
    (ROOT / "rooms-target.json").write_text(json.dumps(target, indent=2) + "\n")
    cli = ROOT / "bin/rooms-run"
    script = ROOT / "bin/rooms-check.py"
    shutil.copy2(Path(__file__).resolve().parent / "remote_check.py", script)
    cli.write_text("#!/usr/bin/env python3\nimport os,sys\n" +
                   "os.execv(sys.executable, [sys.executable, " + repr(str(script)) +
                   ", '--target', " + repr(str(ROOT / "rooms-target.json")) +
                   ", '--patch', sys.argv[1], '--out', sys.argv[2]])\n")
    cli.chmod(0o700)
    backend = {"cli": str(cli), "out": str(ROOT / "result/rooms"), "host": cloud.host(),
               "adapter_sha256": hashlib.sha256(script.read_bytes()).hexdigest(),
               "cli_sha256": hashlib.sha256(cli.read_bytes()).hexdigest(),
               "guest_adapter_sha256": hashlib.sha256(guest_adapter.read_bytes()).hexdigest(),
               "target_sha256": hashlib.sha256((ROOT / "rooms-target.json").read_bytes()).hexdigest(),
               "scope": "local Fleet agents; exact patch executes in a Firecracker Room on GCP; no model credentials transferred"}
    info["rooms"] = backend
    info["verifier_git_layout"] = "separate administration directory before first launch"
    resolved.write_text(json.dumps(info, indent=2) + "\n")
    (ROOT / "RUN.md").write_text(lab.run_brief(ROOT, "codex", backend))
    # Only the verifier needs outbound SSH to the explicitly configured demo.
    # Keep the original workspace-write file boundary and all other overrides.
    wrapper = ROOT / "bin/codex"
    text = wrapper.read_text()
    if text.count(" + sys.argv[1:])") != 1:
        raise RuntimeError("unexpected upstream wrapper; refusing an ambiguous edit")
    text = text.replace(" + sys.argv[1:])", " + (['-c', 'sandbox_workspace_write.network_access=true'] if os.getcwd() == " + repr(str(ROOT / "workbench-verifier")) + " else []) + sys.argv[1:])")
    wrapper.write_text(text)
    info["provider"]["wrapper_sha256"] = hashlib.sha256(wrapper.read_bytes()).hexdigest()
    info["provider"]["network_scope"] = "verifier only: outbound network enabled for demo SSH; author/supervisor remain network-disabled"
    resolved.write_text(json.dumps(info, indent=2) + "\n")
    from audit import entry_unchanged
    if not entry_unchanged(ROOT, backend):
        raise RuntimeError("prepared adapter does not satisfy the upstream freeze check")
    print(ROOT)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("prepare", "run", "resume", "status", "stop"))
    args = parser.parse_args()
    lab = lab_module()
    if args.action == "prepare":
        prepare(lab)
        return
    root = lab.lab_root(str(ROOT))
    if args.action in ("run", "resume"):
        lab.operate(root, resume=args.action == "resume")
    elif args.action == "stop":
        lab.stop(root, requested=True)
    else:
        print(lab.fleet(root, "watch", "status", "--json").stdout)


if __name__ == "__main__":
    main()
