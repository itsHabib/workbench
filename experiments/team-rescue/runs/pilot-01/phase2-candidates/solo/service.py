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


def execute(sql, params=(), fetch=False):
    with LOCK:
        con = sqlite3.connect(DB)
        try:
            cur = con.execute(sql, params)
            rows = cur.fetchall() if fetch else None
            con.commit()
            return rows
        finally:
            con.close()


def initialize(path):
    global DB
    DB = path
    execute("CREATE TABLE IF NOT EXISTS subscriptions (id TEXT PRIMARY KEY, url TEXT NOT NULL)")
    execute("CREATE TABLE IF NOT EXISTS events (id TEXT PRIMARY KEY, payload TEXT NOT NULL)")
    execute("""CREATE TABLE IF NOT EXISTS deliveries (
        id TEXT PRIMARY KEY, event_id TEXT NOT NULL, subscription_id TEXT NOT NULL,
        url TEXT NOT NULL, payload TEXT NOT NULL, status TEXT NOT NULL,
        attempts INTEGER NOT NULL, next_attempt_at INTEGER, activation_attempts INTEGER NOT NULL DEFAULT 0
    )""")
    execute("""CREATE TABLE IF NOT EXISTS attempts (
        delivery_id TEXT NOT NULL, attempt INTEGER NOT NULL, at INTEGER NOT NULL,
        status_code INTEGER, PRIMARY KEY(delivery_id, attempt)
    )""")
    cols = [row[1] for row in execute("PRAGMA table_info(deliveries)", fetch=True)]
    if "activation_attempts" not in cols:
        execute("ALTER TABLE deliveries ADD COLUMN activation_attempts INTEGER NOT NULL DEFAULT 0")


def valid_url(value):
    parsed = urlparse(value)
    return isinstance(value, str) and parsed.scheme in ("http", "https") and bool(parsed.netloc)


def send(delivery):
    body = json.dumps({"event_id": delivery["event_id"], "delivery_id": delivery["id"], "payload": delivery["payload"]}).encode()
    request = urllib.request.Request(delivery["url"], body, {"Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(request, timeout=2) as response:
            return response.status
    except urllib.error.HTTPError as error:
        error.close()
        return error.code
    except OSError:
        return None


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        return

    def body(self):
        length = int(self.headers.get("Content-Length", "0"))
        value = json.loads(self.rfile.read(length))
        if not isinstance(value, dict):
            raise ValueError("request must be an object")
        return value

    def reply(self, status, value):
        data = json.dumps(value, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_POST(self):
        try:
            body = self.body()
            if self.path == "/subscriptions":
                url = body["url"]
                if not valid_url(url):
                    raise ValueError("url must be an HTTP(S) URL")
                ident = uuid.uuid4().hex
                execute("INSERT INTO subscriptions(id,url) VALUES (?,?)", (ident, url))
                self.reply(201, {"id": ident})
                return
            if self.path == "/events":
                ident = body["id"]
                if not isinstance(ident, str) or not ident:
                    raise ValueError("id must be a non-empty string")
                with LOCK:
                    con = sqlite3.connect(DB)
                    try:
                        if con.execute("SELECT 1 FROM events WHERE id=?", (ident,)).fetchone():
                            self.reply(200, {"id": ident, "created": False})
                            return
                        payload = json.dumps(body["payload"])
                        con.execute("INSERT INTO events(id,payload) VALUES (?,?)", (ident, payload))
                        for sid, url in con.execute("SELECT id,url FROM subscriptions"):
                            con.execute("""INSERT INTO deliveries
                                (id,event_id,subscription_id,url,payload,status,attempts,next_attempt_at,activation_attempts)
                                VALUES (?,?,?,?,?,'pending',0,0,0)""", (uuid.uuid4().hex, ident, sid, url, payload))
                        con.commit()
                    finally:
                        con.close()
                self.reply(201, {"id": ident, "created": True})
                return
            if self.path == "/tick":
                now = body["now"]
                if type(now) is not int:
                    raise ValueError("now must be an integer")
                rows = execute("""SELECT id,event_id,url,payload,attempts,activation_attempts
                    FROM deliveries WHERE next_attempt_at IS NOT NULL AND next_attempt_at<=? ORDER BY rowid""", (now,), True)
                count = 0
                for ident, event_id, url, payload_text, total, active in rows:
                    code = send({"id": ident, "event_id": event_id, "url": url, "payload": json.loads(payload_text)})
                    active += 1
                    total += 1
                    count += 1
                    if code is not None and 200 <= code < 300:
                        status, next_at = "succeeded", None
                    elif active >= 3:
                        status, next_at = "failed", None
                    else:
                        status, next_at = "retrying", now + (10 if active == 1 else 20)
                    with LOCK:
                        con = sqlite3.connect(DB)
                        try:
                            con.execute("INSERT INTO attempts VALUES (?,?,?,?)", (ident, total, now, code))
                            con.execute("""UPDATE deliveries SET attempts=?,activation_attempts=?,status=?,next_attempt_at=?
                                WHERE id=? AND attempts=?""", (total, active, status, next_at, ident, total - 1))
                            con.commit()
                        finally:
                            con.close()
                self.reply(200, {"attempted": count})
                return
            if self.path.startswith("/deliveries/") and self.path.endswith("/replay"):
                ident = self.path[len("/deliveries/"):-len("/replay")]
                now = body["now"]
                if not ident or type(now) is not int:
                    raise ValueError("invalid replay request")
                changed = execute("""UPDATE deliveries SET status='pending',next_attempt_at=?,activation_attempts=0
                    WHERE id=? AND status IN ('succeeded','failed')""", (now, ident))
                if not changed:
                    raise ValueError("delivery must be terminal")
                self.reply(200, {"id": ident, "replayed": True})
                return
            self.reply(404, {"error": "not found"})
        except (KeyError, TypeError, ValueError, json.JSONDecodeError, OverflowError, sqlite3.Error) as error:
            self.reply(400, {"error": str(error)})

    def do_GET(self):
        if self.path == "/deliveries":
            rows = execute("""SELECT id,event_id,subscription_id,url,payload,status,attempts,next_attempt_at
                FROM deliveries ORDER BY rowid""", fetch=True)
            result = []
            for row in rows:
                history = execute("SELECT attempt,at,status_code FROM attempts WHERE delivery_id=? ORDER BY attempt", (row[0],), True)
                result.append({"id": row[0], "event_id": row[1], "subscription_id": row[2], "url": row[3],
                    "payload": json.loads(row[4]), "status": row[5], "attempts": row[6], "next_attempt_at": row[7],
                    "history": [{"attempt": a, "at": at, "status_code": code} for a, at, code in history]})
            self.reply(200, {"deliveries": result})
            return
        if self.path == "/metrics":
            statuses = {name: 0 for name in ("pending", "retrying", "succeeded", "failed")}
            for name, count in execute("SELECT status,COUNT(*) FROM deliveries GROUP BY status", fetch=True):
                statuses[name] = count
            values = {"subscriptions": execute("SELECT COUNT(*) FROM subscriptions", fetch=True)[0][0],
                "events": execute("SELECT COUNT(*) FROM events", fetch=True)[0][0],
                "deliveries": execute("SELECT COUNT(*) FROM deliveries", fetch=True)[0][0],
                "attempts": execute("SELECT COUNT(*) FROM attempts", fetch=True)[0][0]}
            self.reply(200, {**values, **statuses})
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
