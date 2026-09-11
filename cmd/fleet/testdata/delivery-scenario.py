"""The fold's delivery and lateness duties, end to end, against the real binary.

A question is sent to an absent address; one fold hands it to the operator's command
once and stamps it; a second fold does nothing; past the reply grace the same fold
writes the unanswered question to the addressee's recorded parent as ordinary mail.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time

here = Path(__file__).resolve().parent
binary = os.environ["FLEET_BIN"]
hook = os.environ.get("FLEET_HOOK", str(here / "hook.py"))

STUB = """import json, os, sys
with open(sys.argv[1], "a") as f:
    f.write(json.dumps({"cwd": os.getcwd(), "prompt": sys.argv[2]}) + "\\n")
"""

with tempfile.TemporaryDirectory(prefix="fleet-delivery-") as tmp:
    root = Path(tmp)
    state, org, lead, seat, boss = [root / n for n in ("state", "org", "lead", "seat", "boss")]
    for path in (state, org, lead, seat, boss):
        path.mkdir()
    (org / "roles.map").write_text(f"{lead} one hub:lead\n{seat} one hub:b seat-1\n{boss} one hub:boss\n")
    stub, launches = root / "stub.py", root / "launches.jsonl"
    stub.write_text(STUB)
    (state / "deliver.json").write_text(json.dumps({
        "hub:lead": {"cwd": str(lead), "cmd": [sys.executable, str(stub), str(launches), "{{prompt}}"],
                     "LATE_TO": "hub:boss"}}))
    env = {**os.environ, "FLEET_STATE": str(state), "ORG_STATE": str(org),
           "FLEET_WATCH": "off", "FLEET_GITHUB": "off", "FLEET_MAIL_GRACE": "0",
           "CODEX_HOME": str(root / "codex-home")}

    def run(cwd, *args, **kw):
        result = subprocess.run([binary, *args], cwd=cwd, env={**env, **kw.pop("extra", {})},
                                text=True, capture_output=True, timeout=30)
        assert result.returncode == 0, (args, result.returncode, result.stderr)
        return result.stdout

    def event(name, sid, cwd):
        payload = {"hook_event_name": name, "session_id": sid, "cwd": str(cwd)}
        result = subprocess.run([sys.executable, hook], input=json.dumps(payload),
                                cwd=cwd, env=env, text=True, capture_output=True, timeout=15)
        assert result.returncode == 0, (name, result.stderr)

    def recorded():
        if not launches.exists():
            return []
        return [json.loads(line) for line in launches.read_text().splitlines() if line.strip()]

    def wait_for_launch(n):
        for _ in range(200):
            if len(recorded()) >= n:
                return recorded()
            time.sleep(0.05)
        raise AssertionError(f"expected {n} launch(es), saw {recorded()}")

    def mailbox(address):
        return json.loads(run(seat, "mail", "--for", address, "--json", "--session", "seat-v1"))

    event("SessionStart", "seat-v1", seat)
    run(seat, "send", "hub:lead", "--id", "q-1", "--kind", "question",
        "--subject", "Which unit?", "--body", "ms or s", "--session", "seat-v1")

    # One fold: one launch, carrying the message, stamped delivered but not acknowledged.
    run(seat, "watch", "--once")
    launch = wait_for_launch(1)[0]
    assert launch["cwd"] == str(lead) or os.path.realpath(launch["cwd"]) == os.path.realpath(lead), launch
    assert "q-1" in launch["prompt"] and "hub:lead" in launch["prompt"], launch
    queued = mailbox("hub:lead")
    assert len(queued) == 1 and queued[0]["id"] == "q-1", queued
    assert queued[0]["delivered_by"] == "fleet:watch" and queued[0]["delivered_at"] > 0, queued[0]
    assert "acked_at" not in queued[0], queued[0]

    # A second fold hands the same message to nobody.
    run(seat, "watch", "--once")
    time.sleep(0.5)
    assert len(recorded()) == 1, recorded()

    # Overlapping one-shot folds share board ownership. A contender either gets
    # the lock after the first finishes or refuses without replacing its state.
    run(seat, "send", "hub:lead", "--id", "q-2", "--kind", "report",
        "--subject", "Second message", "--body", "nothing to answer", "--session", "seat-v1")
    both = [subprocess.Popen([binary, "watch", "--once"], cwd=seat, env=env,
                             text=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            for _ in range(2)]
    completed = 0
    for p in both:
        rc = p.wait(timeout=30)
        error = p.stderr.read()
        assert rc == 0 or (rc == 4 and "watcher lock unavailable" in error), error
        completed += rc == 0
    assert completed >= 1
    time.sleep(0.5)
    carried = [l for l in recorded() if "q-2" in l["prompt"]]
    assert len(carried) == 1, carried

    # Past the reply grace the unanswered question is mail to the recorded parent, once.
    run(seat, "watch", "--once", extra={"FLEET_REPLY_GRACE": "0"})
    reports = mailbox("hub:boss")
    assert len(reports) == 1, reports
    assert reports[0]["kind"] == "report" and reports[0]["from_role"] == "fleet:watch", reports[0]
    assert reports[0]["subject"] == "late: question q-1 to hub:lead", reports[0]
    assert "unacknowledged" in reports[0]["body"], reports[0]
    run(seat, "watch", "--once", extra={"FLEET_REPLY_GRACE": "0"})
    assert len(mailbox("hub:boss")) == 1, mailbox("hub:boss")

print("ok    delivery scenario: one launch per address per fold, stamped once, lateness as mail")
