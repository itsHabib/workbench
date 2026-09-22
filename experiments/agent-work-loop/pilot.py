#!/usr/bin/env python3
"""Portable pilot runner for a prepared agent-work-loop team run.

The first private run left ``fresh=true`` on every later coordinator turn. This
runner records that historical difference and resets fresh after exactly one new
coordinator session is observed. It does not score model output or modify the
acceptance oracle.

Usage: ``python3 pilot.py --run-dir /path/to/prepared-run``. The directory must
contain ``run.json``; the fixture-local ``team.py`` and ``oracle.py`` beside this
runner are used. All raw manifest and event data
stays under the run directory.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess
import signal
import time

HERE = Path(__file__).resolve().parent

REQUESTS = {
    45: "Add JSON document input to POST /imports: {request_id, documents:[{title,body}]}. Preserve exact strings and existing CSV/retry behavior.",
    90: "Add GET /imports/ID/export returning CSV title,body with exact round-trip values, including commas, quotes and newlines. This may be independent of browser work; decide routing.",
    150: "Exercise transient SQLite lock failure: while another connection holds BEGIN EXCLUSIVE, a new import must return an explicit non-2xx within 6 seconds, never memory-only success. After releasing the lock, retry succeeds once and survives service restart. Add a regression test.",
}


def read_json(path):
    return json.loads(path.read_text())


def fixture_file(name):
    candidate = HERE / name
    if not candidate.is_file():
        raise FileNotFoundError(f"fixture is missing {name}")
    return candidate.resolve()


def git_revision(source):
    return subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def event(events, kind, **values):
    with events.open("a") as stream:
        stream.write(json.dumps({"kind": kind, "at": time.time(), **values}) + "\n")
    print(kind, flush=True)


def call(fleet, env, *args):
    return subprocess.run([str(fleet), *args], env=env, capture_output=True, text=True)


def workers(fleet, env):
    result = call(fleet, env, "watch", "status", "--json")
    if result.returncode:
        raise RuntimeError("fleet watch status failed")
    payload = json.loads(result.stdout)
    return payload.get("workers", [])


def jobs(fleet, env, state):
    result = call(fleet, env, "job", "list", "--state", str(state))
    if result.returncode:
        raise RuntimeError("fleet job list failed")
    return json.loads(result.stdout)


def source_edit(path):
    result = subprocess.run(["git", "-C", str(path), "status", "--porcelain"], capture_output=True, text=True)
    if result.returncode:
        return ""
    changed = []
    for line in result.stdout.splitlines():
        if len(line) < 4:
            continue
        name = line[3:].strip().strip('"')
        if name == ".claude/temp" or name.startswith(".claude/temp/"):
            continue
        parts = Path(name).parts
        if "__pycache__" in parts or name.endswith(".pyc"):
            continue
        changed.append(line)
    return "\n".join(changed)


def pilot_environment(state, org_state):
    env = os.environ.copy()
    env.update({"FLEET_STATE": str(state), "ORG_STATE": str(org_state), "FLEET_WATCH": "off"})
    return env


def edit_fresh(config, address, value):
    payload = read_json(config)
    payload[address]["fresh"] = value
    temporary = config.with_suffix(".pilot.tmp")
    temporary.write_text(json.dumps(payload, indent=2) + "\n")
    os.replace(temporary, config)


def coordinator_session(rows, address):
    for row in rows:
        if row.get("address") != address:
            continue
        session = row.get("provider_session") or row.get("last_provider_session")
        if session:
            return session
    return None


def run(run_dir):
    run_dir = run_dir.resolve()
    run_config = run_dir / "run.json"
    config = read_json(run_config)
    source = Path(config["source"]).resolve()
    fleet = Path(config["fleet"]).resolve()
    team = fixture_file("team.py")
    oracle = fixture_file("oracle.py")
    coordinator = run_dir / "coordinator"
    state = (run_dir / "fleet").resolve()
    org_state = run_dir / "org"
    events = run_dir / "pilot-events.jsonl"
    if events.exists() or (run_dir / "pilot-manifest.json").exists():
        raise ValueError('pilot already recorded here; preserve it and prepare a new run directory')
    manifest = {
        "mode": "jobs-integration-pilot",
        "workload_revision": git_revision(source),
        "harness_revision": git_revision(HERE),
        "model": config['model'],
        "turn_budget_usd": config['turn_budget_usd'],
        "reported_budget_stop_usd": config['budget_usd'],
        "deadline_minutes": config['minutes'],
        "oracle_sha256": sha256(oracle),
        "team_sha256": sha256(team),
        "request_schedule_seconds": list(REQUESTS),
        "fault": "worker cancellation after first real source edit; fresh coordinator turn after reclaim",
        "historical_note": "first private run left fresh=true for every subsequent coordinator turn",
        "started_at": time.time(),
    }
    (run_dir / "pilot-manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
    env = pilot_environment(state, org_state)
    requests_dir = run_dir / "REQUESTS.md"
    log_path = run_dir / "pilot-launcher.log"
    log = log_path.open("w")
    process = subprocess.Popen(["python3", str(team), "--run-dir", str(run_dir), "--resume"],
                               cwd=coordinator if coordinator.is_dir() else run_dir,
                               env=env, stdout=log, stderr=log)
    started = time.monotonic()
    sent = set()
    cancelled = None
    fresh_pending = False
    fresh_previous = None
    fresh_reset = False
    interrupted = False
    try:
        while process.poll() is None:
            elapsed = time.monotonic() - started
            for due, text in REQUESTS.items():
                if due in sent or elapsed < due:
                    continue
                with requests_dir.open("a") as stream:
                    stream.write(f"\n- {text}\n")
                sent.add(due)
                event(events, "request_arrived", scheduled_seconds=due, elapsed_seconds=round(elapsed, 2))
            try:
                rows = workers(fleet, env)
                if cancelled is None:
                    for row in rows:
                        address = row.get("address", "")
                        if not address.startswith("worker-"):
                            continue
                        if row.get("provider_state") != "running":
                            continue
                        cwd = row.get("cwd")
                        if not cwd or not source_edit(Path(cwd)):
                            continue
                        result = call(fleet, env, "watch", "cancel", address)
                        cancelled = address
                        event(events, "worker_interrupted", address=address,
                              code=result.returncode, edit_observed=True)
                        break
                if cancelled and not fresh_pending:
                    records = jobs(fleet, env, run_dir / "jobs")
                    if any(len(record.get("attempts", [])) >= 2 for record in records):
                        fresh_previous = coordinator_session(rows, "coordinator:team")
                        config_path = state / "deliver.json"
                        edit_fresh(config_path, "coordinator:team", True)
                        result = call(fleet, env, "watch", "cancel", "coordinator:team")
                        fresh_pending = True
                        event(events, "coordinator_fresh_requested", code=result.returncode)
                if fresh_pending and not fresh_reset:
                    current = coordinator_session(rows, "coordinator:team")
                    if current and current != fresh_previous:
                        edit_fresh(state / "deliver.json", "coordinator:team", False)
                        fresh_reset = True
                        event(events, "coordinator_fresh_reset", session_observed=True)
            except Exception as error:
                event(events, "harness_observation_error", error=str(error))
            time.sleep(0.25)
    except KeyboardInterrupt:
        event(events, "pilot_interrupted")
        interrupted = True
    finally:
        if process.poll() is None:
            process.send_signal(signal.SIGINT if interrupted else signal.SIGTERM)
            try:
                process.wait(timeout=40 if interrupted else 5)
            except subprocess.TimeoutExpired:
                process.kill()
                process.wait(timeout=5)
        log.close()
    event(events, "launcher_exited", code=process.returncode,
          elapsed_seconds=round(time.monotonic() - started, 2),
          fresh_reset=fresh_reset, oracle_sha256=manifest["oracle_sha256"])
    return process.returncode


def main():
    parser = argparse.ArgumentParser(description="Run a prepared Fleet agent-work-loop pilot")
    parser.add_argument("--run-dir", required=True, type=Path,
                        help="prepared run directory containing run.json and team.py")
    args = parser.parse_args()
    return run(args.run_dir)


if __name__ == "__main__":
    raise SystemExit(main())
