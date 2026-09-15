"""Thin Terraform bootstrap adapter; Fleet retains all runtime ownership."""
import argparse
import importlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

HERE = Path(__file__).resolve().parent
ROOT = HERE.parent / ".codex-investigations/fleet-terraform-local"
sys.dont_write_bytecode = True
ROLES = ("supervisor", "author", "verifier")


def load_lab(config):
    source = Path(config["fleet_source"])
    head = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
    dirty = subprocess.check_output(["git", "-C", str(source), "status", "--porcelain"], text=True).strip()
    if head != config["fleet_revision"] or dirty:
        raise RuntimeError("Fleet source must be clean and match fleet_revision")
    sys.path.insert(0, str(source / "cmd/fleet/examples/headless"))
    return importlib.import_module("lab")


def identity(config):
    return {key: value for key, value in config.items() if key != "cards"}


def apply(config):
    if Path(config["root"]) != ROOT or ROOT.is_symlink():
        raise RuntimeError("this experiment only manages its dedicated sibling lab")
    if set(config["cards"]) != set(ROLES):
        raise RuntimeError("this workload requires supervisor, author and verifier cards")
    lab = load_lab(config)
    marker = ROOT / "terraform-input.json"
    if ROOT.exists():
        prior = json.loads(marker.read_text())  # Never adopt a partial or unrelated lab.
        if identity(prior) != identity(config):
            raise RuntimeError("only card edits are supported in place; use a separate experiment directory for a new runtime")
        root = lab.lab_root(str(ROOT))
        status = json.loads(lab.fleet(root, "watch", "status", "--json").stdout)
        if status["watcher"] not in ("never_seen", "stopped") or not lab.exits_collected(root):
            raise RuntimeError("stop Fleet and collect its exits before changing cards")
        for role in config["cards"]:
            target = root / "lanes" / role / "card.md"
            if target.is_symlink() or not target.resolve().is_relative_to(root):
                raise RuntimeError("refusing redirected card path")
        for role, content in config["cards"].items():
            target = root / "lanes" / role / "card.md"
            target.write_text(content)
        lab.card_projection(root, apply=True)
    else:
        with tempfile.TemporaryDirectory(prefix="fleet-terraform-cards-") as scratch:
            for role, content in config["cards"].items():
                (Path(scratch) / (role + ".md")).write_text(content)
            rooms = config["rooms"]
            args = [rooms[key] for key in ("host", "binary", "image", "toolstore")] if rooms else None
            lab.prepare(str(ROOT), scratch, config["fleet_source"], config["provider"], rooms=args)
        # Retain the exact inputs after the temporary import directory disappears.
        imported = ROOT / "imported-cards"
        imported.mkdir()
        for role, content in config["cards"].items():
            (imported / (role + ".md")).write_text(content)
        resolved = ROOT / "resolved.json"
        info = json.loads(resolved.read_text())
        info["card_source"] = str(imported)
        resolved.write_text(json.dumps(info, indent=2) + "\n")
    marker.write_text(json.dumps(config, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=("apply", "run", "resume", "status", "stop", "cards"))
    args = parser.parse_args()
    if args.action == "apply":
        apply(json.loads(os.environ["FLEET_LAB_CONFIG"]))
        return
    config = json.loads((ROOT / "terraform-input.json").read_text())
    lab = load_lab(config)
    root = lab.lab_root(str(ROOT))
    if args.action in ("run", "resume"):
        lab.operate(root, resume=args.action == "resume")
    elif args.action == "stop":
        lab.stop(root, requested=True)
    elif args.action == "cards":
        lab.card_projection(root)
    else:
        print(lab.fleet(root, "watch", "status", "--json").stdout)


if __name__ == "__main__":
    main()
