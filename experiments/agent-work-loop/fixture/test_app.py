#!/usr/bin/env python3
"""Small public smoke suite; it intentionally fails against the seed."""

import json
import subprocess
import sys
import socket
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path


ROOT = Path(__file__).parent
SERVER = ROOT / "server.py"
CSV = "title,body\nTransit Plan,Use the east entrance.\n"


def request(url: str, method: str = "GET", payload: object = None):
    data = None if payload is None else json.dumps(payload).encode()
    request = urllib.request.Request(url, data=data, method=method)
    if data is not None:
        request.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(request, timeout=2) as response:
            return response.status, json.loads(response.read())
    except urllib.error.HTTPError as error:
        return error.code, json.loads(error.read())


def main() -> None:
    with tempfile.TemporaryDirectory() as state:
        with socket.socket() as probe:
            probe.bind(("127.0.0.1", 0))
            port = probe.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        process = subprocess.Popen([sys.executable, str(SERVER), "--port", str(port), "--state", state])
        try:
            deadline = time.time() + 2
            while time.time() < deadline:
                try:
                    status, _ = request(base + "/health")
                    if status == 200:
                        break
                except urllib.error.URLError:
                    time.sleep(0.02)
            else:
                raise AssertionError("server did not become healthy")
            status, result = request(base + "/imports", "POST", {"request_id": "smoke-1", "csv": CSV})
            assert status == 201, (status, result)
        finally:
            process.terminate()
            process.wait(timeout=2)


if __name__ == "__main__":
    main()
