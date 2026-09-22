#!/usr/bin/env python3
"""Run one candidate in an owned process group and reap it if the verifier dies."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import signal
import subprocess
import sys
import time


POLL_SECONDS = 0.05
SHUTDOWN_SECONDS = 2


def terminate_group(child: subprocess.Popen[bytes]) -> None:
    if child.poll() is not None:
        return
    try:
        os.killpg(child.pid, signal.SIGTERM)
    except ProcessLookupError:
        return
    deadline = time.monotonic() + SHUTDOWN_SECONDS
    while child.poll() is None and time.monotonic() < deadline:
        time.sleep(POLL_SECONDS)
    if child.poll() is not None:
        return
    try:
        os.killpg(child.pid, signal.SIGKILL)
    except ProcessLookupError:
        return
    child.wait(timeout=SHUTDOWN_SECONDS)


def run(parent_pid: int, pid_file: Path, command: list[str]) -> int:
    child = subprocess.Popen(command, start_new_session=True)
    pid_file.write_text(str(child.pid))
    stopping = False

    def request_stop(_signum: int, _frame: object) -> None:
        nonlocal stopping
        stopping = True

    signal.signal(signal.SIGTERM, request_stop)
    signal.signal(signal.SIGINT, request_stop)
    try:
        while child.poll() is None:
            if stopping or os.getppid() != parent_pid:
                terminate_group(child)
                break
            time.sleep(POLL_SECONDS)
        return child.wait()
    finally:
        terminate_group(child)
        try:
            pid_file.unlink()
        except FileNotFoundError:
            pass


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--parent-pid", type=int, required=True)
    parser.add_argument("--pid-file", type=Path, required=True)
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    command = args.command
    if command[:1] == ["--"]:
        command = command[1:]
    if not command:
        parser.error("a child command is required after --")
    return run(args.parent_pid, args.pid_file, command)


if __name__ == "__main__":
    raise SystemExit(main())
