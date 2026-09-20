"""Small webhook delivery service with a deliberately missing durability layer."""

import argparse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import urllib.error
import urllib.request
import uuid


SUBSCRIPTIONS = {}
EVENTS = {}
DELIVERIES = {}


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
            if self.path == "/subscriptions":
                subscription_id = uuid.uuid4().hex
                SUBSCRIPTIONS[subscription_id] = body["url"]
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
                self.reply(201, {"id": event_id, "created": True})
                return
            if self.path == "/tick":
                now = body["now"]
                attempted = 0
                for delivery in DELIVERIES.values():
                    due = delivery["next_attempt_at"]
                    if due is None or due > now:
                        continue
                    attempted += 1
                    status = send(delivery)
                    delivery["attempts"] += 1
                    delivery["history"].append({
                        "attempt": delivery["attempts"], "at": now, "status_code": status
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
                self.reply(200, {"attempted": attempted})
                return
            self.reply(404, {"error": "not found"})
        except (KeyError, TypeError, ValueError, json.JSONDecodeError) as error:
            self.reply(400, {"error": str(error)})

    def do_GET(self):
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
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--db", required=True)
    args = parser.parse_args()
    # BUG: --db is accepted but never opened, so every restart loses all work.
    ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
