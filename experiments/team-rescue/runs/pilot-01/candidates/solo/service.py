"""Small durable webhook delivery service."""

import argparse
import json
import sqlite3
import threading
import urllib.error
import urllib.request
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import urlparse


DB = None
LOCK = threading.RLock()


def db_execute(sql, params=(), fetch=False):
    with LOCK:
        connection = sqlite3.connect(DB)
        try:
            cursor = connection.execute(sql, params)
            rows = cursor.fetchall() if fetch else None
            connection.commit()
            return rows
        finally:
            connection.close()


def initialize(path):
    global DB
    DB = path
    db_execute("""
        CREATE TABLE IF NOT EXISTS subscriptions (
            id TEXT PRIMARY KEY,
            url TEXT NOT NULL
        )
    """)
    db_execute("""
        CREATE TABLE IF NOT EXISTS events (
            id TEXT PRIMARY KEY,
            payload TEXT NOT NULL
        )
    """)
    db_execute("""
        CREATE TABLE IF NOT EXISTS deliveries (
            id TEXT PRIMARY KEY,
            event_id TEXT NOT NULL,
            subscription_id TEXT NOT NULL,
            url TEXT NOT NULL,
            payload TEXT NOT NULL,
            status TEXT NOT NULL,
            attempts INTEGER NOT NULL,
            next_attempt_at INTEGER,
            FOREIGN KEY(event_id) REFERENCES events(id),
            FOREIGN KEY(subscription_id) REFERENCES subscriptions(id)
        )
    """)
    db_execute("""
        CREATE TABLE IF NOT EXISTS attempts (
            delivery_id TEXT NOT NULL,
            attempt INTEGER NOT NULL,
            at INTEGER NOT NULL,
            status_code INTEGER,
            PRIMARY KEY(delivery_id, attempt)
        )
    """)


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


def valid_url(value):
    parsed = urlparse(value)
    return (
        isinstance(value, str)
        and parsed.scheme in ("http", "https")
        and bool(parsed.netloc)
    )


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
            if not isinstance(body, dict):
                raise ValueError("request must be an object")
            if self.path == "/subscriptions":
                url = body["url"]
                if not valid_url(url):
                    raise ValueError("url must be an HTTP(S) URL")
                subscription_id = uuid.uuid4().hex
                db_execute(
                    "INSERT INTO subscriptions(id, url) VALUES (?, ?)",
                    (subscription_id, url),
                )
                self.reply(201, {"id": subscription_id})
                return
            if self.path == "/events":
                event_id = body["id"]
                if not isinstance(event_id, str) or not event_id:
                    raise ValueError("id must be a non-empty string")
                with LOCK:
                    connection = sqlite3.connect(DB)
                    try:
                        existing = connection.execute(
                            "SELECT 1 FROM events WHERE id = ?", (event_id,)
                        ).fetchone()
                        if existing:
                            self.reply(200, {"id": event_id, "created": False})
                            return
                        payload = body["payload"]
                        payload_text = json.dumps(payload)
                        connection.execute(
                            "INSERT INTO events(id, payload) VALUES (?, ?)",
                            (event_id, payload_text),
                        )
                        subscriptions = connection.execute(
                            "SELECT id, url FROM subscriptions"
                        ).fetchall()
                        for subscription_id, url in subscriptions:
                            connection.execute(
                                """
                                INSERT INTO deliveries
                                (id, event_id, subscription_id, url, payload,
                                 status, attempts, next_attempt_at)
                                VALUES (?, ?, ?, ?, ?, 'pending', 0, 0)
                                """,
                                (
                                    uuid.uuid4().hex,
                                    event_id,
                                    subscription_id,
                                    url,
                                    payload_text,
                                ),
                            )
                        connection.commit()
                    finally:
                        connection.close()
                self.reply(201, {"id": event_id, "created": True})
                return
            if self.path == "/tick":
                now = body["now"]
                if type(now) is not int:
                    raise ValueError("now must be an integer")
                deliveries = db_execute(
                    """
                    SELECT id, event_id, url, payload, attempts
                    FROM deliveries
                    WHERE next_attempt_at IS NOT NULL
                      AND next_attempt_at <= ?
                    ORDER BY rowid
                    """,
                    (now,),
                    True,
                )
                attempted = 0
                for delivery_id, event_id, url, payload_text, attempts in deliveries:
                    delivery = {
                        "id": delivery_id,
                        "event_id": event_id,
                        "url": url,
                        "payload": json.loads(payload_text),
                    }
                    status_code = send(delivery)
                    attempt = attempts + 1
                    attempted += 1
                    if status_code is not None and 200 <= status_code < 300:
                        status, next_at = "succeeded", None
                    elif attempt >= 3:
                        status, next_at = "failed", None
                    else:
                        delay = 10 if attempt == 1 else 20
                        status, next_at = "retrying", now + delay
                    with LOCK:
                        connection = sqlite3.connect(DB)
                        try:
                            connection.execute(
                                """
                                INSERT INTO attempts
                                (delivery_id, attempt, at, status_code)
                                VALUES (?, ?, ?, ?)
                                """,
                                (delivery_id, attempt, now, status_code),
                            )
                            connection.execute(
                                """
                                UPDATE deliveries
                                SET attempts = ?, status = ?, next_attempt_at = ?
                                WHERE id = ? AND attempts = ?
                                """,
                                (
                                    attempt,
                                    status,
                                    next_at,
                                    delivery_id,
                                    attempts,
                                ),
                            )
                            connection.commit()
                        finally:
                            connection.close()
                self.reply(200, {"attempted": attempted})
                return
            self.reply(404, {"error": "not found"})
        except (KeyError, TypeError, ValueError, json.JSONDecodeError, OverflowError) as error:
            self.reply(400, {"error": str(error)})
        except sqlite3.Error as error:
            self.reply(400, {"error": str(error)})

    def do_GET(self):
        if self.path == "/deliveries":
            rows = db_execute(
                """
                SELECT id, event_id, subscription_id, url, payload,
                       status, attempts, next_attempt_at
                FROM deliveries ORDER BY rowid
                """,
                fetch=True,
            )
            deliveries = []
            for row in rows:
                history = db_execute(
                    """
                    SELECT attempt, at, status_code
                    FROM attempts
                    WHERE delivery_id = ?
                    ORDER BY attempt
                    """,
                    (row[0],),
                    True,
                )
                deliveries.append(
                    {
                        "id": row[0],
                        "event_id": row[1],
                        "subscription_id": row[2],
                        "url": row[3],
                        "payload": json.loads(row[4]),
                        "status": row[5],
                        "attempts": row[6],
                        "next_attempt_at": row[7],
                        "history": [
                            {
                                "attempt": item[0],
                                "at": item[1],
                                "status_code": item[2],
                            }
                            for item in history
                        ],
                    }
                )
            self.reply(200, {"deliveries": deliveries})
            return
        if self.path == "/metrics":
            counts = db_execute(
                "SELECT status, COUNT(*) FROM deliveries GROUP BY status",
                fetch=True,
            )
            statuses = {
                name: 0 for name in ("pending", "retrying", "succeeded", "failed")
            }
            for status, count in counts:
                statuses[status] = count
            subscriptions = db_execute(
                "SELECT COUNT(*) FROM subscriptions", fetch=True
            )[0][0]
            events = db_execute("SELECT COUNT(*) FROM events", fetch=True)[0][0]
            deliveries = db_execute("SELECT COUNT(*) FROM deliveries", fetch=True)[0][0]
            attempts = db_execute("SELECT COUNT(*) FROM attempts", fetch=True)[0][0]
            self.reply(
                200,
                {
                    "subscriptions": subscriptions,
                    "events": events,
                    "deliveries": deliveries,
                    **statuses,
                    "attempts": attempts,
                },
            )
            return
        self.reply(404, {"error": "not found"})


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--db", required=True)
    args = parser.parse_args()
    initialize(args.db)
    ThreadingHTTPServer(("127.0.0.1", args.port), Handler).serve_forever()


if __name__ == "__main__":
    main()
