#!/usr/bin/env python3
"""Offline consumer-boundary reproduction; keeps artifacts in a printed temp dir."""

import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time


SOURCE = Path(__file__).resolve().parents[3]
SCRATCH = Path(tempfile.mkdtemp(prefix="fleet-handoff-poc-")).resolve()
REPO = SCRATCH / "repo"
STATE = SCRATCH / "state"
BIN = SCRATCH / "fleet"
if os.name == "nt":
    BIN = BIN.with_suffix(".exe")
ENV = dict(os.environ, FLEET_STATE=str(STATE), ORG_STATE=str(SCRATCH / "org"),
           FLEET_LANES=str(SCRATCH / "lanes"), FLEET_GITHUB="off", FLEET_WATCH="off")
SESSION = "11111111-1111-1111-1111-111111111111"


def run(args, expected=0, cwd=REPO):
    result = subprocess.run([str(arg) for arg in args], cwd=cwd, env=ENV,
                            text=True, capture_output=True, check=False)
    if result.returncode != expected:
        raise RuntimeError(f"{args}: exit {result.returncode}, expected {expected}\n"
                           f"{result.stdout}{result.stderr}")
    return (result.stdout + result.stderr).strip()


def commit(code, message):
    (REPO / "convert.py").write_text(code)
    run(["git", "add", "convert.py"])
    run(["git", "-c", "user.name=POC", "-c", "user.email=poc@example.invalid",
         "commit", "-qm", message])
    return run(["git", "rev-parse", "HEAD"])


def check(probe, expected):
    run([sys.executable, "-B", "-c", "from convert import minutes; " + probe], expected)


def receipt(head, verdict, observation):
    print(run([BIN, "receipt", head, "implementation", verdict, observation]))


def dispatch(head, expected):
    print(run([BIN, "dispatch", "change", "--as", "verification", "--for", "demo",
               "--requires", "implementation", "--head", head], expected))


def main():
    print(f"Artifacts: {SCRATCH}", flush=True)
    run(["go", "build", "-o", BIN, "./cmd/fleet"], cwd=SOURCE)
    REPO.mkdir()
    run(["git", "init", "-q", "-b", "change"])
    head = commit("def minutes(seconds):\n    return seconds // 60\n", "broken conversion")
    # This is explicitly a fixture for the running demo process, not an agent.
    (STATE / "sessions").mkdir(parents=True)
    (STATE / "sessions" / f"{SESSION}.json").write_text(json.dumps({
        "session": SESSION, "cwd": str(REPO), "branch": "change",
        "last_event_at": time.time(), "pid_kind": "harness", "pid": os.getpid(),
    }))
    print("\n1. Missing implementation evidence refuses the handoff.")
    dispatch(head, 1)
    print("\n2. A real failing regression also refuses it.")
    check("assert minutes(90) == 1.5", 1)
    receipt(head, "fail", "assert minutes(90) == 1.5 failed: integer division")
    dispatch(head, 1)
    print("\n3. Repair, commit, rerun and record: verification can now receive work.")
    head = commit("def minutes(seconds):\n    return seconds / 60\n", "preserve fractional minutes")
    check("assert minutes(90) == 1.5", 0)
    receipt(head, "pass", "assert minutes(90) == 1.5 passed")
    dispatch(head, 0)
    rows = list((STATE / "dispatch").glob("*.json"))
    assert len(rows) == 1, rows
    accepted = rows[0].read_bytes()
    print("\n4. A broader check fails at the same head. The old pass cannot hide it.")
    check("assert minutes(-60) >= 0, 'negative durations must be rejected'", 1)
    receipt(head[:7], "fail", "negative-input probe failed: minutes(-60) returned -1")
    dispatch(head, 1)
    assert rows[0].read_bytes() == accepted, "refusal rewrote the previous handoff"
    print("\n5. Another repair moves the head. Old evidence is insufficient.")
    previous = head
    head = commit("def minutes(seconds):\n    if seconds < 0:\n"
                  "        raise ValueError('negative duration')\n    return seconds / 60\n",
                  "reject negative durations")
    dispatch(previous, 1)
    dispatch(head, 1)
    check("assert minutes(90) == 1.5", 0)
    check("\nrejected = False\ntry:\n    minutes(-60)\nexcept ValueError:\n    rejected = True\nassert rejected, 'accepted negative duration'", 0)
    receipt(head, "pass", "fractional and negative-input checks both passed")
    dispatch(head, 0)
    journal = [json.loads(line) for line in (STATE / "actions.jsonl").read_text().splitlines()]
    decisions = [event["row"] for event in journal if event["action"] == "dispatch_admission"]
    assert [d["result"] for d in decisions] == [
        "refused", "refused", "satisfied", "refused", "refused", "refused", "satisfied"]
    print(f"\nPASS: 7 audited handoff attempts; 2 satisfied. Inspect {STATE}")


if __name__ == "__main__":
    main()
