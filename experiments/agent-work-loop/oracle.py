#!/usr/bin/env python3
"""Independent acceptance oracle for the agent-work-loop fixture."""

import argparse
import csv
import io
import json
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path


CSV_TEXT = "title,body\nAurora Depot,synthetic first body\nMica Station,second body\n"
DOCS = [{"title": "Quartz Gate", "body": "synthetic JSON body"}]


def call(base, path, payload=None, expected=None):
    data = None if payload is None else json.dumps(payload).encode()
    req = urllib.request.Request(base + path, data=data, method="POST" if data else "GET")
    if data:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=3) as response:
            status, raw = response.status, response.read()
    except urllib.error.HTTPError as error:
        status, raw = error.code, error.read()
    try:
        value = json.loads(raw)
    except (ValueError, UnicodeDecodeError):
        value = raw.decode("utf-8", "replace")
    if expected is not None and status != expected:
        raise AssertionError(f"{path}: expected HTTP {expected}, got {status}: {value}")
    return status, value


def checks(base):
    call(base, "/health", expected=200)
    call(base, "/imports", {}, expected=400)
    status, first = call(base, "/imports", {"request_id": "csv-1", "csv": CSV_TEXT}, expected=201)
    import_id = first.get("id") or first.get("import_id")
    if not import_id:
        raise AssertionError("successful import did not return an id")
    _, record = call(base, f"/imports/{import_id}", expected=200)
    rows = record.get("documents", record.get("rows", []))
    if rows != [{"title": "Aurora Depot", "body": "synthetic first body"}, {"title": "Mica Station", "body": "second body"}]:
        raise AssertionError(f"valid rows mismatch: {rows}")
    _, retry = call(base, "/imports", {"request_id": "csv-1", "csv": CSV_TEXT}, expected=200)
    retry_id = retry.get("id") or retry.get("import_id")
    if str(retry_id) != str(import_id):
        raise AssertionError("exact retry created a different import")
    call(base, "/imports", {"request_id": "csv-1", "csv": "title,body\nOther,x\n"}, expected=409)
    call(base, "/imports", {"request_id": "bad-header", "csv": "name,text\nA,B\n"}, expected=400)
    _, json_result = call(base, "/imports", {"request_id": "docs-1", "documents": DOCS}, expected=201)
    export_id = json_result.get("id") or json_result.get("import_id")
    _, exported = call(base, f"/imports/{export_id}/export", expected=200)
    if isinstance(exported, dict):
        raise AssertionError("export endpoint must return CSV bytes")
    parsed = list(csv.DictReader(io.StringIO(exported)))
    if parsed != DOCS or exported.splitlines()[0] != "title,body":
        raise AssertionError("export did not exactly round-trip documents")


def main():
    parser = argparse.ArgumentParser()
    target = parser.add_mutually_exclusive_group(required=True)
    target.add_argument("--url")
    target.add_argument("--app", type=Path)
    parser.add_argument("--port", type=int, default=8765)
    args = parser.parse_args()
    process = None
    state = None
    base = args.url.rstrip("/") if args.url else f"http://127.0.0.1:{args.port}"
    try:
        if args.app:
            state = tempfile.TemporaryDirectory()
            process = subprocess.Popen([sys.executable, str(args.app), "--port", str(args.port), "--state", state.name])
            deadline = time.time() + 5
            while time.time() < deadline:
                try:
                    call(base, "/health", expected=200)
                    break
                except (AssertionError, urllib.error.URLError):
                    time.sleep(0.05)
            else:
                raise AssertionError("app did not become healthy")
        checks(base)
    except Exception as error:
        print(json.dumps({"ok": False, "error": str(error), "url": base}, sort_keys=True))
        return 1
    finally:
        if process:
            process.terminate()
            process.wait(timeout=3)
        if state:
            state.cleanup()
    print(json.dumps({"ok": True, "url": base}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
