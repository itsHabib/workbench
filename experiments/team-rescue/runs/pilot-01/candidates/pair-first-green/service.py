"""Small durable webhook delivery service."""

import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import tempfile
import threading
import urllib.error
import urllib.request
import uuid


SUBSCRIPTIONS = {}
EVENTS = {}
DELIVERIES = {}
DB_PATH = None
STATE_LOCK = threading.RLock()


def load_state(path):
    global SUBSCRIPTIONS, EVENTS, DELIVERIES
    try:
        with open(path, "r", encoding="utf-8") as handle:
            state = json.load(handle)
    except FileNotFoundError:
        return
    except (OSError, json.JSONDecodeError):
        return
    if not isinstance(state, dict):
        return
    SUBSCRIPTIONS = state.get("subscriptions", {})
    EVENTS = state.get("events", {})
    DELIVERIES = state.get("deliveries", {})


def save_state():
    state = {
        "subscriptions": SUBSCRIPTIONS,
        "events": EVENTS,
        "deliveries": DELIVERIES,
    }
    directory = os.path.dirname(os.path.abspath(DB_PATH)) or "."
    fd, temporary = tempfile.mkstemp(prefix=".webhook-", dir=directory)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(state, handle, separators=(",", ":"))
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temporary, DB_PATH)
    except Exception:
        try:
            os.unlink(temporary)
        except OSError:
            pass
        raise


def send(delivery):
    body = json.dumps({
        "event_id": delivery["event_id"],
        "delivery_id": delivery["id"],
        "payload": delivery["payload"],
    }).encode()
    request = urllib.request.Request(
        delivery["url"], body, {"Content-Type": "application/json"}, method="POST"
    )
    try:
        with urllib.request.urlopen(request, timeout=2) as response:
            return response.status
    except urllib.error.HTTPError as error:
        status = error.code
        error.close()
        return status
    except OSError:
        return None


class Handler(BaseHTTPRequestHandler):
    def log_message(self, _format, *_args):
        return

    def json_body(self):
        length = int(self.headers.get("Content-Length", "0"))
        return json.loads(self.rfile.read(length))

    def reply(self, status, value):
        body = json.dumps(value, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        try:
            body = self.json_body()
            with STATE_LOCK:
                if self.path == "/subscriptions":
                    subscription_id = uuid.uuid4().hex
                    SUBSCRIPTIONS[subscription_id] = body["url"]
                    save_state()
                    self.reply(201, {"id": subscription_id})
                    return
                if self.path == "/events":
                    event_id = body["id"]
                    if event_id in EVENTS:
                        self.reply(200, {"id": event_id, "created": False})
                        return
                    EVENTS[event_id] = body["payload"]
                    for subscription_id, url in SUBSCRIPTIONS.items():
                        delivery_id = uuid.uuid4().hex
                        DELIVERIES[delivery_id] = {
                            "id": delivery_id,
                            "event_id": event_id,
                            "subscription_id": subscription_id,
                            "url": url,
                            "payload": body["payload"],
                            "status": "pending",
                            "attempts": 0,
                            "next_attempt_at": 0,
                            "history": [],
                        }
                    save_state()
                    self.reply(201, {"id": event_id, "created": True})
                    return
                if self.path == "/tick":
                    now = body["now"]
                    if type(now) is not int:
                        raise ValueError("now must be an integer")
                    attempted = 0
                    for delivery in DELIVERIES.values():
                        due = delivery["next_attempt_at"]
                        if due is None or due > now:
                            continue
                        attempted += 1
                        status = send(delivery)
                        delivery["attempts"] += 1
                        delivery["history"].append({
                            "attempt": delivery["attempts"],
                            "at": now,
                            "status_code": status,
                        })
                        if status is not None and 200 <= status < 300:
                            delivery["status"] = "succeeded"
                            delivery["next_attempt_at"] = None
                            continue
                        if delivery["attempts"] >= 3:
                            delivery["status"] = "failed"
                            delivery["next_attempt_at"] = None
                            continue
                        delivery["status"] = "retrying"
                        delivery["next_attempt_at"] = now + 10 * (2 ** (delivery["attempts"] - 1))
                    save_state()
                    self.reply(200, {"attempted": attempted})
                    return
                self.reply(404, {"error": "not found"})
        except (KeyError, TypeError, ValueError, json.JSONDecodeError, OSError) as error:
            self.reply(400, {"error": str(error)})

    def do_GET(self):
        with STATE_LOCK:
            if self.path == "/deliveries":
                self.reply(200, {"deliveries": list(DELIVERIES.values())})
                return
            if self.path == "/metrics":
                statuses = {name: 0 for name in ("pending", "retrying", "succeeded", "failed")}
                for delivery in DELIVERIES.values():
                    statuses[delivery["status"]] += 1
                self.reply(200, {
                    "subscriptions": len(SUBSCRIPTIONS),
                    "events": len(EVENTS),
                    "deliveries": len(DELIVERIES),
                    **statuses,
                    "attempts": sum(item["attempts"] for item in DELIVERIES.values()),
                })
                return
            self.reply(404, {"error": "not found"})


def main():
    global DB_PATH
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--db", required=True)
    args = parser.parse_args()
    DB_PATH = args.db
    load_state(DB_PATH)
    ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
