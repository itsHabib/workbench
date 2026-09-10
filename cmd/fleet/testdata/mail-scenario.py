"""Real CLI/hook exchange with replacement sessions, using private state."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile

here = Path(__file__).resolve().parent
binary = os.environ["FLEET_BIN"]
hook = os.environ.get("FLEET_HOOK", str(here / "hook.py"))

with tempfile.TemporaryDirectory(prefix="fleet-mail-") as tmp:
    root = Path(tmp)
    state, org, sender, recipient, foreign = [root / n for n in ("state", "org", "sender", "recipient", "foreign")]
    for path in (state, org, sender, recipient, foreign):
        path.mkdir()
    env = {**os.environ, "FLEET_STATE": str(state), "ORG_STATE": str(org),
           "FLEET_WATCH": "off", "FLEET_GITHUB": "off", "CODEX_HOME": str(root / "codex-home")}
    (org / "roles.map").write_text(f"{sender} one hub:a\n{recipient} one hub:b\n{foreign} two hub:z\n")

    def cli(cwd, *args, body=None):
        return subprocess.run([binary, *args], cwd=cwd, env=env, input=body,
                              text=True, capture_output=True, timeout=15)

    def run(cwd, *args, body=None):
        result = cli(cwd, *args, body=body)
        assert result.returncode == 0, (args, result.returncode, result.stderr)
        return result.stdout

    def event(name, sid, cwd):
        payload = {"hook_event_name": name, "session_id": sid, "cwd": str(cwd)}
        result = subprocess.run([sys.executable, hook], input=json.dumps(payload),
                                cwd=cwd, env=env, text=True, capture_output=True, timeout=15)
        assert result.returncode == 0, (name, result.stderr)
        return result.stdout

    event("SessionStart", "sender-v1", sender)
    event("SessionStart", "recipient-v1", recipient)
    event("SessionEnd", "recipient-v1", recipient)
    full_body = "Use ms or seconds?\nKeep the full second line.\n"
    args = ["send", "hub:b", "--id", "question-1", "--kind", "question",
            "--subject", "Which unit?", "--head", "abc123", "--body", "-", "--session", "sender-v1"]
    first = json.loads(run(sender, *args, body=full_body))
    assert first["from_session"] == "sender-v1" and first["body"] == full_body
    run(sender, "watch", "--once")
    queued = json.loads(run(sender, "mail", "--for", "hub:b", "--unacked", "--json"))
    assert queued == [first]  # The absent recipient stays queued; no launch or stamps.
    assert not any(k.startswith("delivered_") for k in first)

    event("SessionEnd", "sender-v1", sender)
    (state / "sessions" / "sender-v1.json").unlink()
    event("SessionStart", "sender-v2", sender)
    args[-1] = "sender-v2"
    assert json.loads(run(sender, *args, body=full_body)) == first
    conflict = cli(sender, *args, body="different payload")
    assert conflict.returncode == 1 and "different payload" in conflict.stderr
    denied = cli(sender, "send", "hub:z", "--id", "cross-tenant", "--kind", "report",
                 "--subject", "test", "--body", "not allowed")
    assert denied.returncode == 1 and "outside caller tenant" in denied.stderr
    assert cli(sender, "mail", "--for", "hub:z", "--json").returncode == 1
    assert cli(sender, "ack", "question-1").returncode == 1

    line = "[fleet] mail question-1 from hub:a (question): Which unit?"
    assert line in event("SessionStart", "recipient-v2", recipient)
    assert line in event("UserPromptSubmit", "recipient-v2", recipient)
    assert full_body in run(recipient, "mail", "--unacked")
    ack = json.loads(run(recipient, "ack", "question-1"))
    assert ack["acked_by"] == "recipient-v2"
    assert line not in event("UserPromptSubmit", "recipient-v2", recipient)
    assert json.loads(run(sender, *args, body=full_body)) == ack
    answer = json.loads(run(recipient, "send", "hub:a", "--id", "answer-1", "--kind", "answer",
                            "--subject", "Milliseconds", "--body", "Use ms."))
    assert answer["from_session"] == "recipient-v2"
    assert "[fleet] mail answer-1" in event("UserPromptSubmit", "sender-v2", sender)
    assert not (state / "dispatch").exists() and not (state / "leases").exists()
print("  ok    mail: replacement sender retry, queued absent recipient, replacement recipient reads/acks, same-tenant answer, cross-tenant refusal")
