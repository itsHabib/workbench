"""Isolated CLI/hook proof: seats, current assignment, and replacement context."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

binary = os.environ["FLEET_BIN"]
here = Path(__file__).resolve().parent
hook = os.environ.get("FLEET_HOOK", str(here / "hook.py"))

with tempfile.TemporaryDirectory(prefix="fleet-continuity-") as tmp:
    root = Path(tmp)
    state, org, lead, successor, foreign, repo = [root / n for n in
        ("state", "org", "lead", "successor", "foreign", "repo")]
    for path in (state, org, lead, successor, foreign, repo):
        path.mkdir()
    env = {**os.environ, "FLEET_STATE": str(state), "ORG_STATE": str(org),
           "FLEET_WATCH": "off", "FLEET_GITHUB": "off", "CODEX_HOME": str(root / "codex")}

    def git(cwd, *args):
        result = subprocess.run(["git", "-C", str(cwd), *args], env=env,
                                capture_output=True, text=True, timeout=20)
        assert result.returncode == 0, (args, result.stderr)
        return result.stdout.strip()

    git(repo, "init", "-q", "-b", "main")
    git(repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
        "commit", "--allow-empty", "-qm", "fixture")
    a, b = root / "worker-a", root / "worker-b"
    git(repo, "worktree", "add", "-qb", "work-one", str(a))
    git(repo, "worktree", "add", "-qb", "work-two", str(b))
    (org / "roles.map").write_text(
        f"{lead} one hub:lead\n{successor} one hub:lead\n"
        f"{a} one author:sample seat-a\n{b} one author:sample seat-b\n"
        f"{foreign} two hub:foreign\n")

    def cli(cwd, *args):
        return subprocess.run([binary, *args], cwd=cwd, env=env,
                              capture_output=True, text=True, timeout=20)

    def run(cwd, *args):
        result = cli(cwd, *args)
        assert result.returncode == 0, (args, result.returncode, result.stderr)
        return result.stdout

    def event(name, sid, cwd):
        result = subprocess.run([sys.executable, hook], cwd=cwd, env=env,
            input=json.dumps({"hook_event_name": name, "session_id": sid, "cwd": str(cwd)}),
            capture_output=True, text=True, timeout=20)
        assert result.returncode == 0, (name, result.stderr)
        return result.stdout

    def send(cwd, to, mid, text, sid):
        return json.loads(run(cwd, "send", to, "--id", mid, "--kind", "question",
            "--subject", mid, "--body", text, "--session", sid))

    run(lead, "assign", "seat-a", "work-one", "Keep milliseconds", "--for", "hub:lead")
    first_context = event("SessionStart", "worker-a-v1", a)
    assert "Keep milliseconds" in first_context, first_context
    event("SessionStart", "worker-b-v1", b)
    event("SessionStart", "lead-v1", lead)
    event("SessionStart", "foreign-v1", foreign)

    first = send(a, "hub:lead", "units-question", "Which units should work-one use?", "worker-a-v1")
    event("SessionEnd", "worker-a-v1", a)
    replacement_context = event("SessionStart", "worker-a-v2", a)
    assert "Keep milliseconds" in replacement_context, replacement_context
    assert send(a, "hub:lead", "units-question", "Which units should work-one use?", "worker-a-v2") == first

    run(lead, "handoff", "--role", "Parser expects milliseconds", "Answer the worker's units-question", "--session", "lead-v1")
    event("SessionEnd", "lead-v1", lead)
    successor_context = event("SessionStart", "lead-v2", successor)
    assert "Parser expects milliseconds" in successor_context and "units-question" in successor_context, successor_context
    assert "Which units should work-one use?" in run(successor, "mail", "--unacked", "--session", "lead-v2")
    run(successor, "ack", "units-question", "--session", "lead-v2")
    send(successor, "seat-a", "units-answer", "Use milliseconds for work-one.", "lead-v2")
    send(successor, "seat-b", "other-work", "This is for work-two.", "lead-v2")
    amail = json.loads(run(a, "mail", "--json", "--session", "worker-a-v2"))
    bmail = json.loads(run(b, "mail", "--json", "--session", "worker-b-v1"))
    assert [m["id"] for m in amail] == ["units-answer"], amail
    assert [m["id"] for m in bmail] == ["other-work"], bmail
    assert cli(b, "ack", "units-answer", "--session", "worker-b-v1").returncode == 1
    assert cli(a, "send", "hub:foreign", "--id", "bad", "--kind", "report",
               "--subject", "bad", "--body", "cross-tenant", "--session", "worker-a-v2").returncode == 1
    ambiguous = cli(successor, "send", "author:sample", "--id", "ambiguous", "--kind", "report",
                    "--subject", "ambiguous", "--body", "choose no seat", "--session", "lead-v2")
    assert ambiguous.returncode == 1 and "seat-a" in ambiguous.stderr and "seat-b" in ambiguous.stderr, ambiguous.stderr
    assert "units-answer" in event("UserPromptSubmit", "worker-a-v2", a)
    run(a, "ack", "units-answer", "--session", "worker-a-v2")
    event("SessionEnd", "worker-a-v2", a)
    git(a, "checkout", "-qb", "different-work")
    reused_context = event("SessionStart", "worker-a-v3", a)
    assert "Keep milliseconds" not in reused_context, reused_context
    assert "Parser expects milliseconds" not in reused_context, reused_context
    assert not (org / "one").exists(), "ordinary workflow created Org chains"
print("  ok    continuity: distinct seat inboxes, replacement assignment and lead handoff, retry, isolation, reused-seat context")
