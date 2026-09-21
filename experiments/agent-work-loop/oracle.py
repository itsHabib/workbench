#!/usr/bin/env python3
"""Independent acceptance oracle for the agent-work-loop fixture."""

import argparse
import csv
import io
import json
import random
import sqlite3
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.request
from pathlib import Path


def request(base, path, payload=None, raw=None, timeout=3):
    data = raw
    if payload is not None:
        data = json.dumps(payload).encode("utf-8")
    method = "POST" if data is not None else "GET"
    req = urllib.request.Request(base + path, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as response:
            status, body = response.status, response.read()
    except urllib.error.HTTPError as error:
        status, body = error.code, error.read()
    try:
        value = json.loads(body)
    except (ValueError, UnicodeDecodeError):
        value = body.decode("utf-8", "replace")
    return status, value


def expect(base, path, payload=None, raw=None, status=None, timeout=3):
    actual, value = request(base, path, payload, raw, timeout)
    if status is not None and actual != status:
        raise AssertionError(f"{path}: expected HTTP {status}, got {actual}: {value}")
    return actual, value


def documents(seed, count=240):
    rng = random.Random(seed)
    return [{"title": f"{rng.choice(['Aurora', 'Mica', 'Quartz', 'Cedar'])} {seed}-{i:03d}",
             "body": f"synthetic body {rng.randrange(1000000)}"} for i in range(count)]


def csv_text(rows):
    output = io.StringIO(newline="")
    writer = csv.DictWriter(output, fieldnames=["title", "body"], lineterminator="\n")
    writer.writeheader()
    writer.writerows(rows)
    return output.getvalue()


def result_id(value):
    import_id = value.get("id") or value.get("import_id") if isinstance(value, dict) else None
    if import_id is None:
        raise AssertionError(f"successful import did not return an id: {value}")
    return import_id


def import_and_compare(base, seed):
    rows = documents(seed)
    text = csv_text(rows)
    _, first = expect(base, "/imports", {"request_id": f"csv-{seed}", "csv": text}, status=201)
    import_id = result_id(first)
    _, record = expect(base, f"/imports/{import_id}", status=200)
    actual = record.get("documents", record.get("rows"))
    if actual != rows:
        raise AssertionError(f"valid rows mismatch: expected {len(rows)} exact rows, got {actual}")
    return rows, text, import_id


def concurrent_retry(base, text):
    results = []

    def send():
        results.append(request(base, "/imports", {"request_id": "simultaneous-retry", "csv": text}))

    threads = [threading.Thread(target=send) for _ in range(8)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join(timeout=5)
    if len(results) != len(threads):
        raise AssertionError("simultaneous retry request did not complete")
    if sum(status == 201 for status, _ in results) != 1:
        raise AssertionError(f"expected exactly one concurrent create: {[status for status, _ in results]}")
    ids = {str(result_id(value)) for status, value in results if status in (200, 201)}
    if len(ids) != 1:
        raise AssertionError(f"concurrent retry returned duplicate or missing ids: {ids}")


def persistence_fault(base, state_dir, seed):
    database = state_dir / "imports.sqlite"
    if not database.exists():
        database = state_dir / "imports.sqlite3"
    if not database.exists():
        raise AssertionError("persistence fault precondition missing state/imports.sqlite")
    lock = sqlite3.connect(database, timeout=0)
    lock.execute("BEGIN EXCLUSIVE")
    request_id = f"fault-{seed}"
    rows = [{"title": "Fault Probe", "body": "synthetic retry body"}]
    text = csv_text(rows)
    try:
        try:
            status, _ = request(base, "/imports", {"request_id": request_id, "csv": text}, timeout=1)
        except (urllib.error.URLError, TimeoutError):
            status = 599
        if 200 <= status < 300:
            raise AssertionError(f"exclusive SQLite lock produced false success HTTP {status}")
    finally:
        lock.rollback()
        lock.close()
    _, retry = expect(base, "/imports", {"request_id": request_id, "csv": text}, status=201)
    retry_id = result_id(retry)
    _, record = expect(base, f"/imports/{retry_id}", status=200)
    if record.get("documents", record.get("rows")) != rows:
        raise AssertionError("post-fault retry data mismatch")


def checks(base, state_dir, seed, full):
    expect(base, "/health", status=200)
    expect(base, "/imports", {}, status=400)
    expect(base, "/imports", raw=b"{not-json", status=400)
    rows, text, import_id = import_and_compare(base, seed)
    expect(base, "/imports", {"request_id": f"csv-{seed}", "csv": text}, status=200)
    expect(base, "/imports", {"request_id": f"csv-{seed}", "csv": "title,body\nOther,x\n"}, status=409)
    expect(base, "/imports", {"request_id": "bad-header", "csv": "name,text\nA,B\n"}, status=400)
    expect(base, "/imports", {"request_id": "ragged", "csv": "title,body\nA\n"}, status=400)
    expect(base, "/imports", {"request_id": "unterminated", "csv": 'title,body\n"A,B\n'}, status=400)
    concurrent_retry(base, text)
    docs = [{"title": "Quartz Gate", "body": "synthetic JSON body"}, {"title": "Cedar Loop", "body": "second JSON body"}]
    _, json_result = expect(base, "/imports", {"request_id": "docs-1", "documents": docs}, status=201)
    export_id = result_id(json_result)
    _, exported = expect(base, f"/imports/{export_id}/export", status=200)
    if isinstance(exported, dict):
        raise AssertionError("export endpoint must return CSV bytes")
    parsed = list(csv.DictReader(io.StringIO(exported)))
    if exported.splitlines()[0] != "title,body" or parsed != docs:
        raise AssertionError("export did not exactly round-trip documents")
    if not full:
        raise AssertionError("--url mode cannot verify restart persistence or SQLite fault")
    persistence_fault(base, state_dir, seed)
    return rows, import_id


def wait_healthy(base):
    deadline = time.time() + 5
    while time.time() < deadline:
        try:
            expect(base, "/health", status=200)
            return
        except (AssertionError, urllib.error.URLError):
            time.sleep(0.05)
    raise AssertionError("app did not become healthy")


def main():
    parser = argparse.ArgumentParser()
    target = parser.add_mutually_exclusive_group(required=True)
    target.add_argument("--url")
    target.add_argument("--app", type=Path)
    parser.add_argument("--port", type=int, default=8765)
    parser.add_argument("--seed", type=int, default=20260921)
    args = parser.parse_args()
    base = args.url.rstrip("/") if args.url else f"http://127.0.0.1:{args.port}"
    process = None
    state = None
    try:
        if not args.app:
            checks(base, None, args.seed, full=False)
        else:
            state = tempfile.TemporaryDirectory()
            command = [sys.executable, str(args.app), "--port", str(args.port), "--state", state.name]
            process = subprocess.Popen(command)
            wait_healthy(base)
            rows, import_id = checks(base, Path(state.name), args.seed, full=True)
            process.terminate()
            process.wait(timeout=3)
            process = subprocess.Popen(command)
            wait_healthy(base)
            _, record = expect(base, f"/imports/{import_id}", status=200)
            if record.get("documents", record.get("rows")) != rows:
                raise AssertionError("restart persistence data mismatch")
    except Exception as error:
        print(json.dumps({"ok": False, "error": str(error), "url": base, "seed": args.seed}, sort_keys=True))
        return 1
    finally:
        if process:
            process.terminate()
            process.wait(timeout=3)
        if state:
            state.cleanup()
    print(json.dumps({"ok": True, "url": base, "seed": args.seed}, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
