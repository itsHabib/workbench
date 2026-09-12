#!/usr/bin/env bash
# Replay every fleet scenario with Codex's canonical apply_patch event shape.
set -u
here="$(cd "$(dirname "$0")" && pwd)"
FLEET_HOOK="$here/codex-adapter.py" FLEET_CODEX_EVENTS=1 bash "$here/test.sh"
