#!/usr/bin/env bash
# Reproduce the advisory eval end to end: floor binary -> vendor diffs (cached) ->
# derive dataset -> run the local model via local/cmd/eval -> score. Verdict in
# docs/features/local-advisory/EVAL-01.md.
#
# Needs: Ollama up with qwen2.5:7b; gh authenticated (only for the first fetch);
# the graduated local lib at $LOCAL_DIR (default ~/pers/local).
#
#   ./labels/run-eval.sh
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
LOCAL_DIR="${LOCAL_DIR:-$HOME/pers/local}"
cd "$REPO"

[ -x bin/triage-floor.exe ] || go build -o bin/triage-floor.exe ./cmd/triage-floor
ls labels/diffs/*.diff >/dev/null 2>&1 || bash labels/fetch-diffs.sh
node labels/build-dataset.mjs

( cd "$LOCAL_DIR" && go run ./cmd/eval -jsonl -field escalate \
    -prompt  "@$REPO/internal/advisory/prompt.txt" \
    -schema  "@$REPO/internal/advisory/schema.json" \
    -dataset "$REPO/labels/advisory-e01.jsonl" ) > labels/advisory-results.jsonl

node labels/score-advisory.mjs
