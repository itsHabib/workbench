"""Small durable webhook delivery service using only the standard library."""

import argparse
import json
import sqlite3
import threading
import urllib.error
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


DB = None
DB_LOCK = threading.RLock()


def connect(path):
    connection = sqlite3.connect(path, check_same_thread=False)
    connection.row_factory = sqlite3.Row
    connection.executescript(
        """
        CREATE TABLE IF NOT EXISTS subscriptions (
            id TEXT PRIMARY KEY,
            url TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS events (
            id TEXT PRIMARY KEY,
            payload TEXT NOT NULL
        );
        CREATE TABLE IF NOT EXISTS deliveries (
            id TEXT PRIMARY KEY,
            event_id TEXT NOT NULL,
            subscription_id TEXT NOT NULL,
            url TEXT NOT NULL,
            payload TEXT NOT NULL,
            status TEXT NOT NULL,
            attempts INTEGER NOT NULL,
            next_attempt_at INTEGER,
            history TEXT NOT NULL
        );
        """
    )
    connection.commit()
    return connection


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
        error.close()
        return error.code
    except OSError:
        return None


class Handler(BaseHTTPRequestHandler):
    def log_message(self, _format, *_args):
        return

    def json_body(self):
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError as error:
            raise ValueError("invalid content length") from error
        value = json.loads(self.rfile.read(length))
        if not isinstance(value, dict):
            raise ValueError("request body must be an object")
        return value

    def reply(self, status, value):
        body = json.dumps(value, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def bad_request(self, error):
        self.reply(400, {"error": str(error)})

    def do_POST(self):
        try:
            body = self.json_body()
            if self.path == "/subscriptions":
                url = body["url"]
                if not isinstance(url, str) or not url:
                    raise ValueError("url must be a non-empty string")
                subscription_id = uuid.uuid4().hex
                with DB_LOCK:
                    DB.execute(
                        "INSERT INTO subscriptions (id, url) VALUES (?, ?)",
                        (subscription_id, url),
                    )
                    DB.commit()
                self.reply(201, {"id": subscription_id})
                return

            if self.path == "/events":
                event_id = body["id"]
                if not isinstance(event_id, str) or not event_id:
                    raise ValueError("id must be a non-empty string")
                if "payload" not in body:
                    raise ValueError("missing payload")
                with DB_LOCK:
                    existing = DB.execute(
                        "SELECT id FROM events WHERE id = ?", (event_id,)
                    ).fetchone()
                    if existing is not None:
                        self.reply(200, {"id": event_id, "created": False})
                        return
                    payload = json.dumps(body["payload"], separators=(",", ":"))
                    DB.execute("INSERT INTO events (id, payload) VALUES (?, ?)",
                               (event_id, payload))
                    subscriptions = DB.execute(
                        "SELECT id, url FROM subscriptions"
                    ).fetchall()
                    for subscription in subscriptions:
                        DB.execute(
                            """INSERT INTO deliveries
                            (id, event_id, subscription_id, url, payload, status,
                             attempts, next_attempt_at, history)
                            VALUES (?, ?, ?, ?, ?, 'pending', 0, 0, '[]')""",
                            (uuid.uuid4().hex, event_id, subscription["id"],
                             subscription["url"], payload),
                        )
                    DB.commit()
                self.reply(201, {"id": event_id, "created": True})
                return

            if self.path == "/tick":
                now = body["now"]
                if type(now) is not int:
                    raise ValueError("now must be an integer")
                with DB_LOCK:
                    rows = DB.execute(
                        "SELECT * FROM deliveries WHERE next_attempt_at IS NOT NULL "
                        "AND next_attempt_at <= ?",
                        (now,),
                    ).fetchall()
                attempted = 0
                for row in rows:
                    delivery = dict(row)
                    delivery["payload"] = json.loads(delivery["payload"])
                    status_code = send(delivery)
                    attempts = row["attempts"] + 1
                    history = json.loads(row["history"])
                    history.append({"attempt": attempts, "at": now,
                                    "status_code": status_code})
                    succeeded = status_code is not None and 200 <= status_code < 300
                    if succeeded:
                        status, next_at = "succeeded", None
                    elif attempts >= 3:
                        status, next_at = "failed", None
                    else:
                        status = "retrying"
                        next_at = now + 10 * (2 ** (attempts - 1))
                    with DB_LOCK:
                        DB.execute(
                            "UPDATE deliveries SET status = ?, attempts = ?, "
                            "next_attempt_at = ?, history = ? WHERE id = ?",
                            (status, attempts, next_at, json.dumps(history), row["id"]),
                        )
                        DB.commit()
                    attempted += 1
                self.reply(200, {"attempted": attempted})
                return

            self.reply(404, {"error": "not found"})
        except (KeyError, TypeError, ValueError, json.JSONDecodeError) as error:
            self.bad_request(error)
        except sqlite3.Error as error:
            self.reply(500, {"error": str(error)})

    def do_GET(self):
        if self.path == "/deliveries":
            with DB_LOCK:
                rows = DB.execute("SELECT * FROM deliveries").fetchall()
            deliveries = []
            for row in rows:
                delivery = dict(row)
                delivery["payload"] = json.loads(delivery["payload"])
                delivery["history"] = json.loads(delivery["history"])
                deliveries.append(delivery)
            self.reply(200, {"deliveries": deliveries})
            return
        if self.path == "/metrics":
            with DB_LOCK:
                counts = DB.execute(
                    "SELECT status, COUNT(*) AS count FROM deliveries GROUP BY status"
                ).fetchall()
                subscriptions = DB.execute("SELECT COUNT(*) FROM subscriptions").fetchone()[0]
                events = DB.execute("SELECT COUNT(*) FROM events").fetchone()[0]
                deliveries = DB.execute("SELECT COUNT(*) FROM deliveries").fetchone()[0]
                attempts = DB.execute("SELECT COALESCE(SUM(attempts), 0) FROM deliveries").fetchone()[0]
            result = {name: 0 for name in ("pending", "retrying", "succeeded", "failed")}
            for row in counts:
                result[row["status"]] = row["count"]
            self.reply(200, {"subscriptions": subscriptions, "events": events,
                             "deliveries": deliveries, **result, "attempts": attempts})
            return
        self.reply(404, {"error": "not found"})

    def do_PUT(self):
        self.reply(405, {"error": "method not allowed"})

    do_PATCH = do_PUT
    do_DELETE = do_PUT
    do_HEAD = do_PUT
    do_OPTIONS = do_PUT
    do_CONNECT = do_PUT
    do_TRACE = do_PUT


def main():
    global DB
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--db", required=True)
    args = parser.parse_args()
    DB = connect(args.db)
    ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
