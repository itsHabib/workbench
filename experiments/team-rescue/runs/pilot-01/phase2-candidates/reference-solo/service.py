"""Standard-library webhook delivery service with durable SQLite state."""

import argparse
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import sqlite3
import threading
import urllib.error
import urllib.parse
import urllib.request
import uuid


SUBSCRIPTIONS = {}
EVENTS = {}
DELIVERIES = {}
LOCK = threading.RLock()
DATABASE = None


def persist():
    state = json.dumps({
        "subscriptions": SUBSCRIPTIONS,
        "events": EVENTS,
        "deliveries": DELIVERIES,
    }, separators=(",", ":"), allow_nan=False)
    with DATABASE:
        DATABASE.execute(
            "INSERT OR REPLACE INTO state (id, data) VALUES (1, ?)", (state,)
        )


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
    except (OSError, http.client.HTTPException, ValueError):
        return None


def reject_constant(value):
    raise ValueError("invalid JSON constant: " + value)


class Handler(BaseHTTPRequestHandler):
    def log_message(self, _format, *_args):
        return

    def json_body(self):
        length = int(self.headers.get("Content-Length", "0"))
        if length < 0:
            raise ValueError("invalid content length")
        body = json.loads(self.rfile.read(length), parse_constant=reject_constant)
        if not isinstance(body, dict):
            raise ValueError("request must be a JSON object")
        return body

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
            with LOCK:
                status, result = self.post(body)
            self.reply(status, result)
        except (KeyError, TypeError, ValueError) as error:
            self.reply(400, {"error": str(error)})

    def post(self, body):
        if self.path == "/subscriptions":
            url = body["url"]
            if not isinstance(url, str):
                raise ValueError("url must be a string")
            parsed = urllib.parse.urlsplit(url)
            if parsed.scheme not in ("http", "https") or not parsed.hostname:
                raise ValueError("url must be an HTTP URL")
            parsed.port
            if any(ord(char) <= 32 for char in url):
                raise ValueError("invalid URL")
            subscription_id = uuid.uuid4().hex
            SUBSCRIPTIONS[subscription_id] = url
            persist()
            return 201, {"id": subscription_id}
        if self.path == "/events":
            event_id = body["id"]
            if not isinstance(event_id, str) or not event_id:
                raise ValueError("id must be a non-empty string")
            payload = body["payload"]
            if event_id in EVENTS:
                return 200, {"id": event_id, "created": False}
            EVENTS[event_id] = payload
            for subscription_id, url in SUBSCRIPTIONS.items():
                delivery_id = uuid.uuid4().hex
                DELIVERIES[delivery_id] = {
                    "id": delivery_id,
                    "event_id": event_id,
                    "subscription_id": subscription_id,
                    "url": url,
                    "payload": payload,
                    "status": "pending",
                    "attempts": 0,
                    "activation_attempts": 0,
                    "next_attempt_at": 0,
                    "history": [],
                }
            persist()
            return 201, {"id": event_id, "created": True}
        parts = self.path.split("/")
        if len(parts) == 4 and parts[1] == "deliveries" and parts[3] == "replay":
            now = body["now"]
            if type(now) is not int:
                raise ValueError("now must be an integer")
            delivery = DELIVERIES.get(parts[2])
            if delivery is None:
                return 404, {"error": "delivery not found"}
            if delivery["status"] not in ("succeeded", "failed"):
                return 409, {"error": "delivery is not terminal"}
            delivery["status"] = "pending"
            delivery["next_attempt_at"] = now
            delivery["activation_attempts"] = 0
            persist()
            return 200, {"id": delivery["id"], "replayed": True}
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
                activation_attempts = delivery.get("activation_attempts", delivery["attempts"]) + 1
                delivery["activation_attempts"] = activation_attempts
                delivery["attempts"] += 1
                delivery["history"].append({
                    "attempt": delivery["attempts"], "at": now, "status_code": status
                })
                delivery["status"] = "retrying"
                delivery["next_attempt_at"] = now + 10 * activation_attempts
                if status is not None and 200 <= status < 300:
                    delivery["status"] = "succeeded"
                    delivery["next_attempt_at"] = None
                if delivery["status"] != "succeeded" and activation_attempts >= 3:
                    delivery["status"] = "failed"
                    delivery["next_attempt_at"] = None
                persist()
            return 200, {"attempted": attempted}
        return 404, {"error": "not found"}

    def do_GET(self):
        with LOCK:
            self.get()

    def get(self):
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
    global DATABASE
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--db", required=True)
    args = parser.parse_args()
    DATABASE = sqlite3.connect(args.db, check_same_thread=False)
    DATABASE.execute("PRAGMA synchronous = FULL")
    with DATABASE:
        DATABASE.execute("CREATE TABLE IF NOT EXISTS state (id INTEGER PRIMARY KEY, data TEXT NOT NULL)")
    row = DATABASE.execute("SELECT data FROM state WHERE id = 1").fetchone()
    if row is not None:
        state = json.loads(row[0])
        SUBSCRIPTIONS.update(state["subscriptions"])
        EVENTS.update(state["events"])
        DELIVERIES.update(state["deliveries"])
    ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
