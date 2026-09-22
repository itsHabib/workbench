#!/usr/bin/env python3
"""Exercise a local receipt-to-assignment boundary; no models, cloud or watcher."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile


SOURCE = Path(__file__).resolve().parents[4]


def main():
    output = Path(tempfile.mkdtemp(prefix="fleet-relay-"))
    repo, state = output / "subject", output / "fleet-state"
    repo.mkdir()
    env = dict(os.environ, FLEET_STATE=str(state), ORG_STATE=str(output / "org"),
               FLEET_LANES=str(output / "lanes"), FLEET_GITHUB="off", FLEET_WATCH="off")
    calls = []
    fleet = output / "fleet"

    def run(args, *, cwd=repo, input=None, expected=0, contains=None):
        result = subprocess.run([str(a) for a in args], cwd=cwd, env=env,
                                input=input, text=True, capture_output=True, timeout=120)
        calls.append(dict(argv=[str(a) for a in args], cwd=str(cwd),
                          exit_code=result.returncode, stdout=result.stdout, stderr=result.stderr))
        (output / "commands.json").write_text(json.dumps(calls, indent=2) + "\n")
        assert result.returncode == expected, calls[-1]
        if contains:
            assert contains in result.stdout + result.stderr, calls[-1]
        return result.stdout

    run(["go", "build", "-o", fleet, "./cmd/fleet"], cwd=SOURCE)
    run(["git", "init", "-b", "task"])
    run(["git", "config", "user.name", "Relay demo"])
    run(["git", "config", "user.email", "relay@example.invalid"])
    (repo / "go.mod").write_text("module example.invalid/relay\n\ngo 1.22\n")
    (repo / "twice.go").write_text("package demo\nfunc Twice(n int) int { return n + 1 }\n")
    (repo / "twice_test.go").write_text('''package demo
import "testing"
func TestTwice(t *testing.T) {
    if got := Twice(3); got != 6 { t.Fatalf("Twice(3) = %d; want 6", got) }
}
''')
    run(["git", "add", "."])
    run(["git", "commit", "-m", "broken example"])
    old = run(["git", "rev-parse", "HEAD"]).strip()
    producer = "11111111-1111-1111-1111-111111111111"
    receiver = "22222222-2222-2222-2222-222222222222"

    def session(sid, directory):
        # Synthetic harness event in isolated state, not an AI worker launch.
        event = dict(hook_event_name="SessionStart", session_id=sid,
                     cwd=str(directory), source="startup")
        run([fleet, "hook", "claude"], cwd=directory, input=json.dumps(event))

    session(producer, repo)
    # Use a separate checkout for the receiving participant.
    receiver_tree = output / "receiver"
    run(["git", "worktree", "add", "--detach", receiver_tree, old])
    session(receiver, receiver_tree)

    def request(head, *, expected=1, contains=None, request_id="verify-1"):
        return run([fleet, "request", "task", "--id", request_id, "--as", "verify",
                    "--worker", receiver, "--for", "lead:demo",
                    "--brief", "Check Twice on the pinned commit; report counterexamples.",
                    "--head", head, "--requires", "unit"], expected=expected, contains=contains)

    def receipt(head, verdict, observable):
        run([fleet, "receipt", head, "unit", verdict, observable, "--session", producer])

    request(old, contains="read history")
    failed = run(["go", "test", "-count=1", "./..."], expected=1)
    (output / "failed-test.txt").write_text(failed)
    receipt(old, "fail", "go test: Twice(3) = 4; want 6; see failed-test.txt")
    request(old, contains="latest receipt is fail")

    (repo / "twice.go").write_text("package demo\nfunc Twice(n int) int { return n * 2 }\n")
    run(["git", "add", "."])
    run(["git", "commit", "-m", "fix doubling"])
    head = run(["git", "rev-parse", "HEAD"]).strip()
    request(old, contains="stale input revision")
    request(head, contains="read history")
    passed = run(["go", "test", "-count=1", "./..."])
    (output / "passed-test.txt").write_text(passed)
    receipt(head, "pass", "go test: TestTwice passed; see passed-test.txt")

    latest = state / "receipts" / f"{head}.unit.json"
    published = latest.read_bytes()
    damaged = json.loads(published)
    damaged["verdict"] = "fail"
    latest.write_text(json.dumps(damaged))  # Explicit fault injection, isolated state only.
    request(head, contains="history and published receipt disagree")
    assert not list((state / "dispatch").glob("*.json")), "refusals published an assignment"
    latest.write_bytes(published)  # Restore the exact fixture bytes, not new evidence.

    request(head, expected=0, contains="Queued")
    rows = list((state / "dispatch").glob("*.json"))
    assert len(rows) == 1
    admitted = rows[0].read_bytes()
    packet = json.loads(admitted)
    assert packet["entry"]["head"] == head
    assert packet["entry"]["receipts"][0]["verdict"] == "pass"
    # Each command is a new process. Re-run after a simulated lost reply.
    request(head, expected=0, contains="already recorded")
    assert rows[0].read_bytes() == admitted, "retry changed the admission"
    request(old, contains="request ID already has different work")

    # Exercise the MCP face with the same ID and evidence contract too.
    rpc = dict(jsonrpc="2.0", id=1, method="tools/call", params=dict(name="fleet_request",
               arguments=dict(change="task", id="verify-1", worker=receiver,
                              brief=packet["brief"], head=head, requires="unit", cwd=str(repo),
                              **{"as": "verify", "for": "lead:demo"})))
    reply = json.loads(run([fleet, "mcp"], input=json.dumps(rpc) + "\n"))
    assert not reply["result"].get("isError"), reply
    assert rows[0].read_bytes() == admitted

    # The receiving participant checks out the pinned revision and runs its check.
    run(["git", "checkout", "--detach", head], cwd=receiver_tree)
    observed = run(["go", "test", "-count=1", "./..."], cwd=receiver_tree)
    (output / "receiver-test.txt").write_text(observed)
    run([fleet, "receipt", head, "verify", "pass",
         "Separate checkout: TestTwice passed; see receiver-test.txt", "--session", receiver],
        cwd=receiver_tree)
    run([fleet, "done", head, "--kind", "verify"], contains="DONE")
    status = json.loads(run([fleet, "status", "--json"]))
    assert status["tasks"][0]["status"] == "Queued"  # Activity is not inferred from checks.

    run(["git", "commit", "--allow-empty", "-m", "later revision"])
    drift = json.loads(run([fleet, "status", "--json"]))
    assert drift["tasks"][0]["status"] == "Status needs checking"
    assert rows[0].read_bytes() == admitted
    (output / "admission.json").write_bytes(admitted)
    source_head = run(["git", "rev-parse", "HEAD"], cwd=SOURCE).strip()
    source_dirty = run(["git", "status", "--porcelain"], cwd=SOURCE)
    summary = dict(source_head=source_head, source_dirty=bool(source_dirty),
                   subject_old_head=old, subject_admitted_head=head,
                   admission_sha256=hashlib.sha256(admitted).hexdigest(),
                   cases=["missing receipt refused", "actual failing test refused", "stale revision refused",
                          "new revision needs new receipt", "contradictory publication refused",
                          "retry after fixture repair admitted", "process restart replay unchanged",
                          "conflicting retry refused", "MCP replay unchanged",
                          "separate checkout consumed packet and recorded verification",
                          "later revision visible without rewriting admission"],
                   synthetic_sessions=True, model_calls=0, cloud_runs=0,
                   limitation="Mechanical local contract demo; not independent agent review or merge authority.")
    (output / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
    print(json.dumps(summary, indent=2))
    print(f"\nEvidence retained at {output}")


if __name__ == "__main__":
    main()
