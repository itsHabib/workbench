#!/usr/bin/env bash
# Vendor FULL unified diffs for a corpus of PRs, so evals run offline and
# reproducibly. One file per PR: labels/diffs/<repo>-<num>.diff.
#
# Full, not capped: the deterministic floor (triage-floor) must see the whole diff
# or it misses a signal-bearing file that sorts late (a migration, a lockfile, a
# sensitive path) and under-calls the tier. Only the *advisory model* input gets
# capped — build-dataset.mjs does that, on read, so the floor stays honest.
#
#   ./labels/fetch-diffs.sh                       # Experiment-01 corpus (cached; FORCE=1 to refetch)
#   ./labels/fetch-diffs.sh labels/corpus-heldout.tsv   # any corpus TSV (col 1: <repo>#<num>)
set -euo pipefail

cd "$(dirname "$0")/.."
mkdir -p labels/diffs

# PRs are drawn from the corpus TSV (col 1: <repo>#<num>). Owner is fixed.
# Match only real data rows (repo#num) — the leading '#' comment header must not slip through.
OWNER="itsHabib"
CORPUS="${1:-labels/corpus-e01.tsv}"
prs=$(awk -F'\t' '$1 ~ /^[a-z][a-z0-9-]*#[0-9]+$/ {print $1}' "$CORPUS")

for pr in $prs; do
  repo="${pr%%#*}"
  num="${pr##*#}"
  out="labels/diffs/${repo}-${num}.diff"
  if [ -f "$out" ] && [ "${FORCE:-0}" != "1" ]; then
    echo "skip  $pr (cached; FORCE=1 to refetch)"
    continue
  fi
  if gh pr diff "$num" -R "$OWNER/$repo" > "$out" 2>/dev/null; then
    lines=$(wc -l < "$out" | tr -d ' ')
    echo "ok    $pr -> $out (${lines} lines)"
  else
    echo "FAIL  $pr (gh pr diff failed)" >&2
    rm -f "$out"
  fi
done
