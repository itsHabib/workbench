#!/usr/bin/env python3
"""Small stdlib-only import app seed for the live-agent workload.

This is intentionally incomplete.  The workload agents must turn the route
stubs into a durable import service while keeping the documented contract.
"""

import argparse
import json
import sqlite3
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path


SCHEMA = """
CREATE TABLE IF NOT EXISTS imports (
    import_id INTEGER PRIMARY KEY AUTOINCREMENT,
    request_id TEXT NOT NULL UNIQUE,
    source_format TEXT NOT NULL,
    payload TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)
"""


def initialize_database(state_dir: Path) -> Path:
    state_dir.mkdir(parents=True, exist_ok=True)
    database = state_dir / "imports.sqlite3"
    with sqlite3.connect(database) as connection:
        connection.execute(SCHEMA)
    return database


class Handler(BaseHTTPRequestHandler):
    database: Path

    def send_json(self, status: int, value: object) -> None:
        body = json.dumps(value, sort_keys=True).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self) -> None:  # noqa: N802
        if self.path == "/health":
            self.send_json(200, {"ok": True})
            return
        if self.path == "/":
            body = (
                "<!doctype html><title>Import fixture</title>"
                "<h1>Document imports</h1>"
                "<p>Upload CSV via the JSON API at <code>/imports</code>.</p>"
            ).encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        # TODO: implement GET /imports/ID and /imports/ID/export.
        self.send_json(404, {"error": "not found"})

    def do_POST(self) -> None:  # noqa: N802
        if self.path != "/imports":
            self.send_json(404, {"error": "not found"})
            return
        length = int(self.headers.get("Content-Length", "0"))
        try:
            request = json.loads(self.rfile.read(length))
        except (json.JSONDecodeError, UnicodeDecodeError):
            self.send_json(400, {"error": "invalid JSON"})
            return
        if not isinstance(request, dict) or not request.get("request_id"):
            self.send_json(400, {"error": "request_id is required"})
            return
        # TODO: validate CSV/document formats, persist records, and implement
        # exact retry/conflicting request_id semantics.
        self.send_json(501, {"error": "import implementation pending"})

    def log_message(self, format: str, *args: object) -> None:
        return


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--state", type=Path, required=True)
    args = parser.parse_args()
    Handler.database = initialize_database(args.state)
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()


if __name__ == "__main__":
    main()
