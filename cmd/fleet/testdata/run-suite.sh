#!/usr/bin/env bash
# Build the fleet binary and run the reference suite against it through the shims.
# The suite is test.sh from the reference implementation, unchanged where possible; every
# `python3 hook.py` / `python3 fleet.py` it issues execs the Go binary via the shims beside it.
#
#   bash cmd/fleet/testdata/run-suite.sh            # test.sh
#   bash cmd/fleet/testdata/run-suite.sh codex      # test-codex.sh (the Codex adapter face)
set -u
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../../.." && pwd)"
( cd "$root" && go build -o "$here/fleet.bin" ./cmd/fleet/ ) || { echo "build failed"; exit 1; }
export FLEET_BIN="$here/fleet.bin"
export FLEET_WATCH=off FLEET_GITHUB=off   # the suite must not spawn watchers from its hundreds of SessionStarts
case "${1:-}" in
  codex) exec bash "$here/test-codex.sh" ;;
  *)     exec bash "$here/test.sh" ;;
esac
