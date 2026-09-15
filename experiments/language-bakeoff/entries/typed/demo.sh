#!/usr/bin/env bash
# One-command canned demo of the typed-API POC: the six bakeoff cases plus
# interrupted-run recovery. Everything runs inside a fresh temporary
# directory that is removed on exit (KEEP=1 keeps it). Nothing outside it
# is written except Go's own build cache.
set -euo pipefail

repo=$(cd "$(dirname "$0")" && pwd)
demo=$(mktemp -d "${TMPDIR:-/tmp}/wb-typed-demo.XXXXXX")
if [[ "${KEEP:-}" == 1 ]]; then
  echo "keeping $demo"
else
  trap 'rm -rf "$demo"' EXIT
fi
mkdir -p "$demo/bin" "$demo/plans"
(cd "$repo" && go build -o "$demo/bin/" ./cmd/textpipe ./cmd/scratchpipe)
export PATH="$demo/bin:$PATH"
cd "$demo"

T3=$'the quick brown fox\njumps over the lazy dog\nthen naps\n'
T4=$'hello typed workbench\n'
T5=$'retry me please\nnow\n'

section() { printf '\n\033[1m== %s\033[0m\n' "$*"; }
note() { printf '   %s\n' "$*"; }
show() { printf '\n$'; printf ' %q' "$@"; printf '\n'; }
# expect CODE CMD...: run CMD, and stop the demo unless it exits with CODE.
expect() {
  local want=$1 code=0
  shift
  show "$@"
  "$@" || code=$?
  if [[ $code -ne $want ]]; then
    printf 'DEMO FAILED: exit %d, want %d\n' "$code" "$want"
    exit 1
  fi
  if [[ $code -ne 0 ]]; then printf '[exit %d, as expected]\n' "$code"; fi
}
# check DESCRIPTION CMD...: an executable assertion, not just output to read.
check() {
  local what=$1
  shift
  if "$@"; then printf '   check: %s\n' "$what"; return; fi
  printf 'DEMO FAILED: %s\n' "$what"
  exit 1
}
# shell SNIPPET: show a shell snippet (redirection, pipes) as typed, then run it.
shell() {
  printf '\n$ %s\n' "$1"
  sh -c "$1"
}
same() { [[ "$1" == "$2" ]]; }
identity() { ls -i "$@"; cksum ws/.wb/evidence.jsonl; }

section "0. Source -> normalized intent"
note "The workflow is ordinary Go (workflows/textpipe/textpipe.go):"
sed -n '/^func Declare/,/^}/p' "$repo/workflows/textpipe/textpipe.go"
note "Evaluating it yields plain data. The engine consumes only this, never Go code:"
expect 0 textpipe intent

section "1. Initial plan and apply in a freshly created directory"
mkdir ws
expect 0 textpipe plan -dir ws -out plans/1.json
check "planning performed no effects (ws/ is still empty)" same "$(ls -A ws)" ""
expect 0 textpipe apply -dir ws -plan plans/1.json
expect 0 cat ws/upper.txt ws/wordcount.txt
check "upper.txt is input.txt uppercased" same "$(cat ws/upper.txt)" "$(tr '[:lower:]' '[:upper:]' <ws/input.txt)"

section "2. Unchanged re-apply: nothing repeats"
before=$(identity ws/input.txt ws/upper.txt ws/wordcount.txt)
expect 0 textpipe apply -dir ws
check "no file rewritten (same inodes) and no evidence added" same "$before" "$(identity ws/input.txt ws/upper.txt ws/wordcount.txt)"

section "3. Input change -> downstream plan"
expect 0 textpipe plan -dir ws -var "text=$T3" -out plans/3.json
expect 0 textpipe apply -dir ws -plan plans/3.json
check "wordcount.txt follows the new input" same "$(cat ws/wordcount.txt)" "11"

section "4. External edit between plan and apply"
expect 0 textpipe plan -dir ws -var "text=$T4" -out plans/4.json
note "Someone edits the input after the plan was reviewed:"
shell 'echo "edited by hand" > ws/input.txt'
expect 3 textpipe apply -dir ws -plan plans/4.json
check "the stale plan applied nothing" same "$(cat ws/input.txt)" "edited by hand"
note "Re-planning shows the real change, including the overwrite of the hand edit:"
expect 0 textpipe plan -dir ws -var "text=$T4" -out plans/4b.json
expect 0 textpipe apply -dir ws -plan plans/4b.json

section "5. Injected partial failure, then retry"
expect 1 env WB_FAULT=fail:transform.wordcount textpipe apply -dir ws -var "text=$T5"
done_before=$(ls -i ws/input.txt ws/upper.txt)
expect 0 textpipe plan -dir ws -var "text=$T5" -out plans/5.json
expect 0 textpipe apply -dir ws -plan plans/5.json
check "the retry did not rewrite input.txt or upper.txt" same "$done_before" "$(ls -i ws/input.txt ws/upper.txt)"
check "wordcount.txt is now correct" same "$(cat ws/wordcount.txt)" "4"

section "5b. Interrupted run: killed after an effect, before its evidence"
mkdir ws-crash
expect 137 env WB_FAULT=crash:transform.upper textpipe apply -dir ws-crash
note "What a fresh caller finds: the output is on disk, the evidence has a start with no finish."
shell 'cut -c1-90 ws-crash/.wb/evidence.jsonl'
expect 0 textpipe plan -dir ws-crash
expect 0 textpipe apply -dir ws-crash

section "6. Third adapter: a disposable directory"
note "The extension is a new package plus a workflow that uses it. Its complete code:"
expect 0 cat "$repo/adapters/scratchdir/scratchdir.go"
note "The workflow composes textpipe with it (workflows/scratchpipe/scratchpipe.go):"
sed -n '/^func Define/,/^}/p' "$repo/workflows/scratchpipe/scratchpipe.go"
note "Registration in cmd/scratchpipe/main.go, and the foundation operations added for it:"
grep -n 'Adapters:' "$repo/cmd/scratchpipe/main.go"
for op in DirEmpty Remove Rename; do sed -n "/^\/\/ $op /,/^}/p" "$repo/foundation/workspace.go"; done
core_deps=$(cd "$repo" && go list -deps ./wb ./engine ./cli ./foundation | grep -c '/adapters/' || true)
check "the API, planner, CLI and foundation import no adapter" same "$core_deps" "0"

mkdir ws6
expect 0 scratchpipe apply -dir ws6
shell 'echo "not declared anywhere" > ws6/work/stray.txt'
expect 0 scratchpipe plan -dir ws6 -var generation=2 -out plans/6.json
expect 0 scratchpipe apply -dir ws6 -plan plans/6.json
check "the replace wiped the directory, stray file included" test ! -e ws6/work/stray.txt
check "the report was rebuilt inside it" test -s ws6/work/report.txt

note "It refuses to adopt, and so to ever delete, a non-empty directory it did not create:"
mkdir -p ws6b/work
echo mine >ws6b/work/precious.txt
expect 1 scratchpipe plan -dir ws6b
check "precious.txt is untouched" same "$(cat ws6b/work/precious.txt)" "mine"

section "All checks passed."
