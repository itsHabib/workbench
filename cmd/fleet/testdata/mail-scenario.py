"""Role-addressed send -> hook -> ack -> absent-role delivery, in private state."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

here = Path(__file__).resolve().parent
binary = os.environ["FLEET_BIN"]
hook = os.environ.get("FLEET_HOOK", str(here / "hook.py"))

with tempfile.TemporaryDirectory(prefix="fleet-mail-") as tmp:
    root = Path(tmp)
    state, org, sender, recipient = [root / n for n in ("state", "org", "sender", "recipient")]
    for path in (state, org, sender, recipient):
        path.mkdir()
    env = {**os.environ, "FLEET_STATE": str(state), "ORG_STATE": str(org),
           "FLEET_MAIL_GRACE": "0s", "FLEET_WATCH": "off", "FLEET_GITHUB": "off"}
    (org / "roles.map").write_text(f"{sender} one hub:a\n{recipient} one hub:b\n")
    (state / "contacts.json").write_text(json.dumps({"hub:a": ["hub:b"]}))

    def run(cwd, *args):
        result = subprocess.run([binary, *args], cwd=cwd, env=env, text=True,
                                capture_output=True, timeout=15)
        assert result.returncode == 0, (args, result.returncode, result.stderr)
        return result.stdout

    def event(name, sid, cwd):
        payload = {"hook_event_name": name, "session_id": sid, "cwd": str(cwd)}
        result = subprocess.run([sys.executable, hook], input=json.dumps(payload),
                                cwd=cwd, env=env, text=True, capture_output=True, timeout=15)
        assert result.returncode == 0, (name, result.stderr)
        return result.stdout

    event("SessionStart", "mail-sender", sender)
    args = ["send", "hub:b", "--id", "question-1", "--kind", "question",
            "--subject", "Which unit?", "--body", "Use ms or seconds?", "--session", "mail-sender"]
    first = json.loads(run(sender, *args))
    assert json.loads(run(sender, *args)) == first
    line = "[fleet] mail question-1 from hub:a (question): Which unit?"
    assert line in event("SessionStart", "mail-recipient", recipient)
    assert line in event("UserPromptSubmit", "mail-recipient", recipient)
    assert len(json.loads(run(recipient, "mail", "--unacked", "--json"))) == 1
    ack = json.loads(run(recipient, "ack", "question-1"))
    assert ack["acked_by"] == "mail-recipient"
    assert line not in event("UserPromptSubmit", "mail-recipient", recipient)
    event("SessionEnd", "mail-recipient", recipient)
    args[3] = "question-2"
    run(sender, *args)
    true = shutil.which("true")
    assert true, "reference scenario needs true on PATH"
    (state / "deliver.json").write_text(json.dumps({"hub:b": {"cwd": str(recipient), "cmd": [true]}}))
    run(sender, "watch", "--once")
    records = {r["id"]: r for r in json.loads(run(sender, "mail", "--for", "hub:b", "--json"))}
    assert "delivered_at" not in records["question-1"]
    assert records["question-2"]["delivered_at"] > 0
    assert records["question-2"]["delivered_by"].startswith("watch-")
    assert "acked_at" not in records["question-2"]
    run(sender, "watch", "--once")
    observed = [json.loads(line) for line in (state / "watch" / "observed.jsonl").read_text().splitlines()]
    assert len([r for r in observed if r.get("what") == "mail-delivery-started"]) == 1
    assert not (state / "leases").exists()
print("  ok    mail: retry-safe send, SessionStart/prompt line, ack, one stub delivery stamp")
