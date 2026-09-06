#!/usr/bin/env python3
"""Shim: the suite runs `python3 hook.py` with the event on stdin; this execs the Go binary."""
import os, sys
here = os.path.dirname(os.path.abspath(__file__))
exe = os.environ.get("FLEET_BIN") or os.path.join(here, "fleet.bin")
os.execv(exe, [exe, "hook", "claude"] + sys.argv[1:])
