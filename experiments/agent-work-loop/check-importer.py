#!/usr/bin/env python3
"""Caller-owned API + browser check. Run from the import application's checkout.

Needs Node, Playwright and its Chromium browser (or PLAYWRIGHT_CHANNEL=chrome).
Exit 0 accepts, 1 rejects the application, 2 indicates a check setup failure.
"""
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import time
import urllib.request

HERE = Path(__file__).resolve().parent


def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1', 0))
        return sock.getsockname()[1]


def main():
    if subprocess.run(['node', '-e', "require.resolve('playwright')"], capture_output=True).returncode:
        print('Install Playwright outside worker checkouts and set NODE_PATH.', file=sys.stderr)
        return 2
    app = Path.cwd() / 'server.py'
    port = free_port()
    base = f'http://127.0.0.1:{port}'
    with tempfile.TemporaryDirectory() as state:
        process = subprocess.Popen([sys.executable, str(app), '--port', str(port), '--state', state],
                                   stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            for _ in range(100):
                try:
                    urllib.request.urlopen(base + '/health', timeout=1).close()
                    break
                except OSError:
                    time.sleep(.05)
            result = subprocess.run(['node', str(HERE / 'browser-check.cjs'), base])
            if result.returncode:
                return result.returncode
        finally:
            process.terminate()
            process.wait(timeout=5)
    # Keep the frozen API oracle independent; this wrapper only composes checks.
    return subprocess.run([sys.executable, str(HERE / 'oracle.py'), '--app', str(app),
                           '--port', str(free_port()), '--seed', '119337']).returncode


if __name__ == '__main__':
    raise SystemExit(main())
