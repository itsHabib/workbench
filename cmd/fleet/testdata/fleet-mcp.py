#!/usr/bin/env python3
"""Shim: the suite runs `python3 fleet-mcp.py` over stdio; this execs the Go binary's MCP face."""
import os, sys
here = os.path.dirname(os.path.abspath(__file__))
exe = os.environ.get("FLEET_BIN") or os.path.join(here, "fleet.bin")
os.execv(exe, [exe, "mcp"] + sys.argv[1:])
