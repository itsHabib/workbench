#!/usr/bin/env python3
"""Shim: the suite runs `python3 codex-adapter.py` with a Codex event on stdin; this execs the Go
binary's codex face, which translates and evaluates in-process."""
import os, sys
here = os.path.dirname(os.path.abspath(__file__))
exe = os.environ.get("FLEET_BIN") or os.path.join(here, "fleet.bin")
os.execv(exe, [exe, "hook", "codex"] + sys.argv[1:])
