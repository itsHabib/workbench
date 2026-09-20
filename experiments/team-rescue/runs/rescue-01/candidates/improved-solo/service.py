"""Small durable webhook delivery service."""

import argparse
from copy import deepcopy
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
import tempfile
import threading
import urllib.error
import urllib.parse
import urllib.request
import uuid

SUBSCRIPTIONS = {}
EVENTS = {}
DELIVERIES = {}
DB_PATH = None
STATE_LOCK = threading.RLock()


def _valid_state(state):
    if not isinstance(state, dict):
        return False
    subscriptions = state.get("subscriptions")
    events = state.get("events")
    deliveries = state.get("deliveries")
    if not isinstance(subscriptions, dict) or not isinstance(events, dict):
        return False
    if not isinstance(deliveries, dict):
        return False
    if any(not isinstance(k, str) or not isinstance(v, str) for k, v in subscriptions.items()):
        return False
    if any(not isinstance(k, str) for k in events):
        return False
    required = {"id", "event_id", "subscription_id", "url", "payload", "status", "attempts", "next_attempt_at", "history"}
    statuses = {"pending", "retrying", "succeeded", "failed"}
    for key, delivery in deliveries.items():
        if not isinstance(key, str) or not isinstance(delivery, dict) or set(delivery) != required:
            return False
        if any(not isinstance(delivery[field], str) for field in ("id", "event_id", "subscription_id", "url")):
            return False
        if delivery["status"] not in statuses or type(delivery["attempts"]) is not int or delivery["attempts"] < 0:
            return False
        if delivery["next_attempt_at"] is not None and type(delivery["next_attempt_at"]) is not int:
            return False
        if not isinstance(delivery["history"], list):
            return False
        for entry in delivery["history"]:
            if not isinstance(entry, dict) or set(entry) != {"attempt", "at", "status_code"}:
                return False
            if type(entry["attempt"]) is not int or type(entry["at"]) is not int:
                return False
            if entry["status_code"] is not None and type(entry["status_code"]) is not int:
                return False
    return True


def load_state(path):
    global SUBSCRIPTIONS, EVENTS, DELIVERIES
    try:
        with open(path, "r", encoding="utf-8") as handle:
            state = json.load(handle)
    except (FileNotFoundError, OSError, json.JSONDecodeError):
        return
    if _valid_state(state):
        SUBSCRIPTIONS = state["subscriptions"]
        EVENTS = state["events"]
        DELIVERIES = state["deliveries"]


def save_state():
    state = {"subscriptions": SUBSCRIPTIONS, "events": EVENTS, "deliveries": DELIVERIES}
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
    body = json.dumps({"event_id": delivery["event_id"], "delivery_id": delivery["id"], "payload": delivery["payload"]}).encode()
    request = urllib.request.Request(delivery["url"], body, {"Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(request, timeout=2) as response:
            return response.status
    except urllib.error.HTTPError as error:
        status = error.code
        error.close()
        return status
    except OSError:
        return None


def valid_loopback_url(value):
    if not isinstance(value, str) or not value:
        return False
    try:
        parsed = urllib.parse.urlparse(value)
        hostname = parsed.hostname
        port = parsed.port
    except ValueError:
        return False
    if parsed.scheme != "http" or parsed.username or parsed.password or parsed.query or parsed.fragment or not hostname:
        return False
    return hostname.lower() in {"127.0.0.1", "localhost", "::1"} and port is not None and 1 <= port <= 65535


class Handler(BaseHTTPRequestHandler):
    def log_message(self, _format, *_args):
        return

    def send_error(self, code, message=None, explain=None):
        self.reply(405 if code == 501 else code, {"error": message or "request error"})

    def json_body(self):
        content_type = self.headers.get("Content-Type", "")
        if content_type.split(";", 1)[0].strip().lower() != "application/json":
            raise ValueError("Content-Type must be application/json")
        try:
            length = int(self.headers.get("Content-Length", "-1"))
        except ValueError as error:
            raise ValueError("invalid Content-Length") from error
        if length < 0:
            raise ValueError("missing Content-Length")
        value = json.loads(self.rfile.read(length))
        if not isinstance(value, dict):
            raise ValueError("JSON body must be an object")
        return value

    def reply(self, status, value):
        body = json.dumps(value, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def mutate(self, operation):
        snapshot = (deepcopy(SUBSCRIPTIONS), deepcopy(EVENTS), deepcopy(DELIVERIES))
        try:
            result = operation()
            save_state()
            return result
        except Exception:
            SUBSCRIPTIONS.clear(); SUBSCRIPTIONS.update(snapshot[0])
            EVENTS.clear(); EVENTS.update(snapshot[1])
            DELIVERIES.clear(); DELIVERIES.update(snapshot[2])
            raise

    def do_POST(self):
        try:
            body = self.json_body()
            with STATE_LOCK:
                if self.path == "/subscriptions":
                    url = body.get("url")
                    if not valid_loopback_url(url):
                        raise ValueError("url must be an HTTP loopback URL")
                    subscription_id = uuid.uuid4().hex
                    result = self.mutate(lambda: (SUBSCRIPTIONS.__setitem__(subscription_id, url) or {"id": subscription_id}))
                    self.reply(201, result); return
                if self.path == "/events":
                    event_id = body.get("id")
                    if not isinstance(event_id, str) or not event_id or "payload" not in body:
                        raise ValueError("id must be a non-empty string and payload is required")
                    if event_id in EVENTS:
                        self.reply(200, {"id": event_id, "created": False}); return
                    def create():
                        EVENTS[event_id] = body["payload"]
                        for subscription_id, url in SUBSCRIPTIONS.items():
                            delivery_id = uuid.uuid4().hex
                            DELIVERIES[delivery_id] = {"id": delivery_id, "event_id": event_id, "subscription_id": subscription_id, "url": url, "payload": body["payload"], "status": "pending", "attempts": 0, "next_attempt_at": 0, "history": []}
                        return {"id": event_id, "created": True}
                    self.reply(201, self.mutate(create)); return
                if self.path == "/tick":
                    now = body.get("now")
                    if type(now) is not int:
                        raise ValueError("now must be an integer")
                    def tick():
                        attempted = 0
                        for delivery in DELIVERIES.values():
                            if delivery["next_attempt_at"] is None or delivery["next_attempt_at"] > now:
                                continue
                            attempted += 1; status = send(delivery); delivery["attempts"] += 1
                            delivery["history"].append({"attempt": delivery["attempts"], "at": now, "status_code": status})
                            if status is not None and 200 <= status < 300:
                                delivery["status"] = "succeeded"; delivery["next_attempt_at"] = None; continue
                            if delivery["attempts"] >= 3:
                                delivery["status"] = "failed"; delivery["next_attempt_at"] = None; continue
                            delivery["status"] = "retrying"; delivery["next_attempt_at"] = now + 10 * (2 ** (delivery["attempts"] - 1))
                        return {"attempted": attempted}
                    self.reply(200, self.mutate(tick)); return
                self.reply(404, {"error": "not found"})
        except (KeyError, TypeError, ValueError, json.JSONDecodeError, OSError) as error:
            self.reply(400, {"error": str(error)})

    def do_GET(self):
        with STATE_LOCK:
            if self.path == "/deliveries":
                self.reply(200, {"deliveries": list(DELIVERIES.values())}); return
            if self.path == "/metrics":
                statuses = {name: 0 for name in ("pending", "retrying", "succeeded", "failed")}
                for delivery in DELIVERIES.values(): statuses[delivery["status"]] += 1
                self.reply(200, {"subscriptions": len(SUBSCRIPTIONS), "events": len(EVENTS), "deliveries": len(DELIVERIES), **statuses, "attempts": sum(item["attempts"] for item in DELIVERIES.values())}); return
            self.reply(404, {"error": "not found"})


def main():
    global DB_PATH
    parser = argparse.ArgumentParser(); parser.add_argument("--port", type=int, required=True); parser.add_argument("--db", required=True)
    args = parser.parse_args(); DB_PATH = args.db; load_state(DB_PATH)
    ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
