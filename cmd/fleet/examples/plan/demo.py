#!/usr/bin/env python3
"""Run against a built Fleet binary. All repositories and state are disposable."""
import json
import os
import re
from pathlib import Path
import subprocess
import sys
import tempfile

binary = str(Path(sys.argv[1]).resolve())
root = Path(tempfile.mkdtemp(prefix="fleet-plan-demo-"))
env = dict(os.environ, FLEET_STATE=str(root / "state"), ORG_STATE=str(root / "org"),
           ORG_TENANT="plan-demo", FLEET_WATCH="off", FLEET_GITHUB="off")
(root / "org").mkdir()

def run(*args, expected=0):
    result = subprocess.run([binary, *args], cwd=root, env=env, text=True, capture_output=True)
    print(result.stdout, end="")
    if result.stderr:
        print(result.stderr, end="")
    assert result.returncode == expected, (args, result.returncode, expected)
    return result.stdout

work = []
for name in ("ivy", "rooms", "roxiq"):
    repo = root / name
    repo.mkdir()
    def git(*args):
        subprocess.run(["git", "-C", str(repo), *args], check=True, capture_output=True, env=env)
    git("init", "-b", "demo")
    git("-c", "user.name=Demo", "-c", "user.email=demo@example.invalid", "commit", "--allow-empty", "-m", "fixture")
    work.append(dict(name=name, repo=str(repo), change="demo", **{"for": "lead:demo", "as": "draft"},
                     brief=f"Document one existing {name} workflow; stop at a draft PR.", due_at="2030-01-01T12:00:00Z"))
intent = root / "morning.json"
intent.write_text(json.dumps(dict(schema="fleet-work.v0", work=work), indent=2))
plan_file = root / "plan.json"
run("plan", str(intent), "--out", str(plan_file))
plan = json.loads(plan_file.read_text())
assert not (root / "state").exists(), "preview wrote Fleet state"

# An unreadable Rooms row represents an intervening store conflict. No live state.
dispatch = root / "state" / "dispatch"
dispatch.mkdir(parents=True)
# Reproduce Fleet's Safe filename transformation for this ASCII fixture.
key = plan["actions"][1]["repo_id"] + "__demo__draft"
blocked = dispatch / (re.sub(r"[^A-Za-z0-9_.-]", "__", key) + ".json")
blocked.mkdir()
results = json.loads(run("apply", str(plan_file), "--expect-digest", plan["digest"], expected=1))
assert [r["status"] for r in results] == ["recorded", "conflict", "not attempted"]
first = {p.name: p.read_bytes() for p in dispatch.glob("*.json") if p.is_file()}
blocked.rmdir()
results = json.loads(run("apply", str(plan_file), "--expect-digest", plan["digest"]))
assert [r["status"] for r in results] == ["already recorded", "recorded", "recorded"]
assert all((dispatch / name).read_bytes() == data for name, data in first.items())

work[0]["brief"] = "Changed scope with the same accountable role."
work[0]["due_at"] = "2030-01-02T12:00:00Z"
intent.write_text(json.dumps(dict(schema="fleet-work.v0", work=work), indent=2))
changed_file = root / "changed-plan.json"
run("plan", str(intent), "--out", str(changed_file))
changed = json.loads(changed_file.read_text())
assert changed["actions"][0]["action"] == "conflict"
run("apply", str(changed_file), "--expect-digest", changed["digest"], expected=1)
assert all((dispatch / name).read_bytes() == data for name, data in first.items())
print(f"PASS: partial retry preserved original bytes; changed brief/deadline refused.\nArtifacts: {root}")
