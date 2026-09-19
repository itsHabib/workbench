#!/usr/bin/env python3
"""Small local CSV import desk."""
import argparse
import csv
import hashlib
import io
import json
import os
import threading
import uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlparse

FIELDS = ("id", "name", "email")
MAX_BODY_BYTES = 10 * 1024 * 1024

class RequestConflict(ValueError):
    pass

def validate_csv(source):
    errors, records = [], []
    reader = csv.reader(io.StringIO(source), strict=True)
    try:
        header = next(reader)
    except StopIteration:
        return records, [{"row": 1, "message": "CSV is empty; expected id, name, email header"}]
    except csv.Error as exc:
        return records, [{"row": 1, "message": "malformed CSV: " + str(exc)}]
    if header != list(FIELDS):
        return records, [{"row": 1, "message": "header must be exactly id,name,email"}]
    try:
        for row_number, values in enumerate(reader, start=2):
            if len(values) != len(FIELDS):
                errors.append({"row": row_number, "message": "expected exactly 3 columns: id, name, email"})
                continue
            record = dict(zip(FIELDS, values))
            missing = [field for field in FIELDS if not record[field].strip()]
            if missing:
                errors.append({"row": row_number, "message": "empty field: " + ", ".join(missing)})
                continue
            email = record["email"]
            if " " in email:
                errors.append({"row": row_number, "message": "email must not contain spaces"})
                continue
            if email.count("@") != 1 or not all(email.split("@")):
                errors.append({"row": row_number, "message": "email must contain one @ with text on both sides"})
                continue
            records.append(record)
    except csv.Error as exc:
        return [], [{"row": max(reader.line_num, 1), "message": "malformed CSV: " + str(exc)}]
    return records, errors

class ImportStore:
    def __init__(self, data_dir):
        self.path = Path(data_dir) / "imports.json"
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self.lock = threading.Lock()
        self.items = self._load()
    def _load(self):
        try:
            with self.path.open(encoding="utf-8") as stream:
                value = json.load(stream)
            return value if isinstance(value, list) else []
        except (FileNotFoundError, json.JSONDecodeError):
            return []
    def _save(self):
        temporary = self.path.with_suffix(".tmp")
        with temporary.open("w", encoding="utf-8") as stream:
            json.dump(self.items, stream, indent=2)
        os.replace(temporary, self.path)
    def create_or_replay(self, source, request_id=None):
        fingerprint = hashlib.sha256(source.encode("utf-8")).hexdigest()
        with self.lock:
            if request_id:
                for existing in self.items:
                    if existing.get("request_id") != request_id:
                        continue
                    if existing.get("fingerprint") == fingerprint:
                        return existing, True
                    raise RequestConflict("request_id is already used for different CSV")
            records, errors = validate_csv(source)
            item = {"id": uuid.uuid4().hex[:12], "status": "completed", "records": records, "errors": errors}
            if request_id:
                item["request_id"] = request_id
                item["fingerprint"] = fingerprint
            self.items.insert(0, item)
            self._save()
        return item, False

    def create(self, source, request_id=None):
        item, _ = self.create_or_replay(source, request_id)
        return item
    def get(self, import_id):
        with self.lock:
            return next((item for item in self.items if item["id"] == import_id), None)
    def summaries(self):
        with self.lock:
            return [{"id": item["id"], "status": item["status"], "record_count": len(item["records"]), "error_count": len(item["errors"])} for item in self.items]

class Handler(BaseHTTPRequestHandler):
    server_version = "ImportDesk/1.0"
    def _discard(self, length):
        remaining = length
        while remaining > 0:
            chunk = self.rfile.read(min(65536, remaining))
            if not chunk:
                return
            remaining -= len(chunk)

    def _json(self, status, value):
        payload = json.dumps(value).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)
    def do_GET(self):
        path = urlparse(self.path).path.rstrip("/") or "/"
        if path == "/health":
            self._json(200, {"status": "ok"})
            return
        if path == "/":
            payload = self.server.ui.encode("utf-8")
            self.send_response(200)
            self.send_header("Content-Type", "text/html; charset=utf-8")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
            return
        if path == "/imports":
            self._json(200, {"imports": self.server.store.summaries()})
            return
        if path.startswith("/imports/"):
            item = self.server.store.get(path.split("/", 2)[2])
            self._json(200 if item else 404, item or {"error": "import not found"})
            return
        self._json(404, {"error": "not found"})
    def do_POST(self):
        if urlparse(self.path).path.rstrip("/") != "/imports":
            self._json(404, {"error": "not found"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if length < 0:
                raise ValueError("invalid content length")
            if length > MAX_BODY_BYTES:
                self._discard(length)
                self._json(413, {"error": "request body exceeds 10 MiB limit"})
                return
            raw = self.rfile.read(length)
            body = json.loads(raw)
            source = body["csv"]
            if not isinstance(source, str):
                raise ValueError("csv must be a string")
            request_id = body.get("request_id")
            if request_id is not None and not isinstance(request_id, str):
                raise ValueError("request_id must be a string")
        except (ValueError, KeyError, TypeError, json.JSONDecodeError):
            self._json(400, {"error": "request must include csv as a string"})
            return
        try:
            item, replay = self.server.store.create_or_replay(source, request_id)
        except RequestConflict as exc:
            self._json(409, {"error": str(exc)})
            return
        self._json(200 if replay else 201,
                   {"id": item["id"], "status": item["status"]})
    def log_message(self, *_args):
        return

def load_ui():
    return (Path(__file__).parent / "index.html").read_text(encoding="utf-8")

def main():
    parser = argparse.ArgumentParser(description="Run the local import desk")
    parser.add_argument("--port", type=int, default=8000)
    parser.add_argument("--data-dir", default="./data")
    args = parser.parse_args()
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    server.store = ImportStore(args.data_dir)
    server.ui = load_ui()
    print(f"Import desk listening on http://127.0.0.1:{args.port}", flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()

if __name__ == "__main__":
    main()
