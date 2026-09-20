import json
from pathlib import Path
import subprocess
import sys
import tempfile
import textwrap
import unittest


HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
import verifier  # noqa: E402


GOOD_SERVICE = r'''
import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import threading
import urllib.error
import urllib.request
import uuid


def fresh():
    return {"subscriptions": {}, "events": {}, "deliveries": {}}


class App:
    def __init__(self, path):
        self.path = Path(path)
        self.lock = threading.Lock()
        self.state = json.loads(self.path.read_text()) if self.path.exists() else fresh()

    def save(self):
        temporary = self.path.with_suffix(self.path.suffix + ".tmp")
        temporary.write_text(json.dumps(self.state, separators=(",", ":")))
        temporary.replace(self.path)

    def subscribe(self, url):
        subscription_id = uuid.uuid4().hex
        self.state["subscriptions"][subscription_id] = url
        self.save()
        return subscription_id

    def event(self, event_id, payload):
        if event_id in self.state["events"]:
            return False
        self.state["events"][event_id] = payload
        for subscription_id, url in self.state["subscriptions"].items():
            delivery_id = uuid.uuid4().hex
            self.state["deliveries"][delivery_id] = {
                "id": delivery_id, "event_id": event_id,
                "subscription_id": subscription_id, "url": url,
                "payload": payload, "status": "pending", "attempts": 0,
                "activation_attempts": 0, "next_attempt_at": 0, "history": [],
            }
        self.save()
        return True

    def send(self, delivery):
        value = {"event_id": delivery["event_id"], "delivery_id": delivery["id"],
                 "payload": delivery["payload"]}
        request = urllib.request.Request(
            delivery["url"], json.dumps(value).encode(),
            {"Content-Type": "application/json"}, method="POST")
        try:
            with urllib.request.urlopen(request, timeout=1) as response:
                return response.status
        except urllib.error.HTTPError as error:
            status = error.code
            error.close()
            return status
        except OSError:
            return None

    def tick(self, now):
        attempted = 0
        for delivery in self.state["deliveries"].values():
            due = delivery["next_attempt_at"]
            if due is None or due > now:
                continue
            attempted += 1
            status = self.send(delivery)
            delivery["attempts"] += 1
            delivery["activation_attempts"] += 1
            delivery["history"].append({"attempt": delivery["attempts"], "at": now,
                                         "status_code": status})
            if status is not None and 200 <= status < 300:
                delivery["status"] = "succeeded"
                delivery["next_attempt_at"] = None
                continue
            active = delivery["activation_attempts"]
            if active >= 3:
                delivery["status"] = "failed"
                delivery["next_attempt_at"] = None
                continue
            delivery["status"] = "retrying"
            delivery["next_attempt_at"] = now + 10 * (2 ** (active - 1))
        self.save()
        return attempted

    def replay(self, delivery_id, now):
        delivery = self.state["deliveries"].get(delivery_id)
        if delivery is None or delivery["status"] not in ("failed", "succeeded"):
            return False
        delivery["status"] = "pending"
        delivery["activation_attempts"] = 0
        delivery["next_attempt_at"] = now
        self.save()
        return True

    def metrics(self):
        counts = {key: 0 for key in ("pending", "retrying", "succeeded", "failed")}
        for delivery in self.state["deliveries"].values():
            counts[delivery["status"]] += 1
        return {"subscriptions": len(self.state["subscriptions"]),
                "events": len(self.state["events"]),
                "deliveries": len(self.state["deliveries"]), **counts,
                "attempts": sum(row["attempts"] for row in self.state["deliveries"].values())}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, _format, *_args):
        return

    def reply(self, status, value):
        data = json.dumps(value, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def body(self):
        return json.loads(self.rfile.read(int(self.headers.get("Content-Length", "0"))))

    def do_GET(self):
        with self.server.app.lock:
            if self.path == "/deliveries":
                self.reply(200, {"deliveries": list(self.server.app.state["deliveries"].values())})
                return
            if self.path == "/metrics":
                self.reply(200, self.server.app.metrics())
                return
            self.reply(404, {"error": "not found"})

    def do_POST(self):
        try:
            body = self.body()
            with self.server.app.lock:
                if self.path == "/subscriptions":
                    self.reply(201, {"id": self.server.app.subscribe(body["url"])})
                    return
                if self.path == "/events":
                    made = self.server.app.event(body["id"], body["payload"])
                    self.reply(201 if made else 200, {"id": body["id"], "created": made})
                    return
                if self.path == "/tick":
                    self.reply(200, {"attempted": self.server.app.tick(body["now"])})
                    return
                prefix = "/deliveries/"
                suffix = "/replay"
                if self.path.startswith(prefix) and self.path.endswith(suffix):
                    delivery_id = self.path[len(prefix):-len(suffix)]
                    if self.server.app.replay(delivery_id, body["now"]):
                        self.reply(200, {"id": delivery_id, "replayed": True})
                        return
                    self.reply(409, {"error": "delivery is not terminal or does not exist"})
                    return
                self.reply(404, {"error": "not found"})
        except (KeyError, TypeError, ValueError, json.JSONDecodeError) as error:
            self.reply(400, {"error": str(error)})


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--db", required=True)
    args = parser.parse_args()
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    server.app = App(args.db)
    server.serve_forever()


if __name__ == "__main__":
    main()
'''


class VerifierTests(unittest.TestCase):
    def fixture(self, source=GOOD_SERVICE):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        workspace = Path(temporary.name)
        (workspace / "service.py").write_text(textwrap.dedent(source))
        return workspace

    def test_independent_good_fixture_passes_both_phases(self):
        result = verifier.check(self.fixture(), phase=2)
        self.assertTrue(result["passed"], json.dumps(result, indent=2))
        self.assertEqual([row["name"] for row in result["checks"]][-1], "replay")

    def test_seed_fails_restart_persistence(self):
        result = verifier.check(HERE / "workload", phase=1)
        by_name = {row["name"]: row for row in result["checks"]}
        self.assertFalse(result["passed"])
        self.assertFalse(by_name["restart_persistence"]["passed"])
        self.assertIn("lost across restart", by_name["restart_persistence"]["detail"])
        self.assertTrue(by_name["api_fanout_and_idempotency"]["passed"])

    def test_negative_control_with_four_attempt_cap_is_rejected(self):
        broken = GOOD_SERVICE.replace("if active >= 3:", "if active >= 4:")
        result = verifier.check(self.fixture(broken), phase=1)
        by_name = {row["name"]: row for row in result["checks"]}
        self.assertFalse(result["passed"])
        self.assertFalse(by_name["retry_isolation_and_cap"]["passed"])

    def test_negative_control_cannot_fake_delivery_with_healthy_state(self):
        broken = GOOD_SERVICE.replace("status = self.send(delivery)", "status = 204")
        result = verifier.check(self.fixture(broken), phase=1)
        by_name = {row["name"]: row for row in result["checks"]}
        self.assertFalse(result["passed"])
        self.assertFalse(by_name["api_fanout_and_idempotency"]["passed"])
        self.assertIn("fanout receipts", by_name["api_fanout_and_idempotency"]["detail"])

    def test_cli_prints_json_and_uses_failure_exit(self):
        workspace = self.fixture("print('not a server')\n")
        completed = subprocess.run(
            [sys.executable, str(HERE / "verifier.py"), str(workspace), "--phase", "1"],
            text=True, capture_output=True, timeout=30,
        )
        self.assertEqual(completed.returncode, 1)
        receipt = json.loads(completed.stdout)
        self.assertFalse(receipt["passed"])
        self.assertEqual(receipt["phase"], 1)

    def test_invalid_phase_fails_without_starting_candidate(self):
        result = verifier.check(self.fixture(), phase=3)
        self.assertFalse(result["passed"])
        self.assertEqual(result["checks"][0]["name"], "configuration")


if __name__ == "__main__":
    unittest.main()
