#!/usr/bin/env bash
# Replay the same recorded comments through both consumers before and after.
set -euo pipefail

root=$(git -C "$(dirname "${BASH_SOURCE[0]}")" rev-parse --show-toplevel)
base=10b066cac0bb959ca3dfa7dc2d77886eed277e78
baseline_dir=$(mktemp -d)
trap 'rm -rf "$baseline_dir"' EXIT

git -C "$root" archive "$base" | tar -xf - -C "$baseline_dir"
for path in \
  contracts/reviewpanel/testdata/workbench-334-comments.json \
  cmd/review/completion_fixture_test.go \
  cmd/gate/internal/evidence/completion_fixture_test.go; do
  mkdir -p "$baseline_dir/$(dirname "$path")"
  cp "$root/$path" "$baseline_dir/$path"
done

printf 'Before: %s\n' "$base"
if (cd "$baseline_dir" && go test ./cmd/review ./cmd/gate/internal/evidence \
  -run '^TestRecordedReviewCompletion$' -count=1 -v) >"$baseline_dir/output.txt" 2>&1; then
  cat "$baseline_dir/output.txt"
  echo 'Expected the original review observer to miss the recorded completions.' >&2
  exit 1
fi
cat "$baseline_dir/output.txt"
# Reject unrelated build/test failures as a reproduction of the known mismatch.
grep -Fq 'review.observe: completed=[] missing=[claude codex]' "$baseline_dir/output.txt"
grep -Fq -- '--- PASS: TestRecordedReviewCompletion' "$baseline_dir/output.txt"

printf '\nAfter: current checkout\n'
cd "$root"
go test ./cmd/review ./cmd/gate/internal/evidence \
  -run '^TestRecordedReviewCompletion$' -count=1 -v
