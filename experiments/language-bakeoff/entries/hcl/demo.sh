#!/usr/bin/env bash
# One-command canned demo of the six common cases in a freshly created
# disposable directory. Every effect stays inside that directory, plus the
# `tr` child processes wb starts. KEEP=1 keeps the directory afterwards.
set -euo pipefail

repo="$(cd "$(dirname "$0")" && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/wb-demo.XXXXXX")"
if [ "${KEEP:-}" != 1 ]; then trap 'rm -rf "$work"' EXIT; fi

(cd "$repo" && go build -o "$work/bin/wb" ./cmd/wb)
cp -R "$repo/examples" "$work/examples"
mkdir "$work/ws"
cd "$work/ws"
export PATH="$work/bin:$PATH"

case_() { printf '\n\n########## %s\n' "$*"; }
note() { printf '\n# %s\n' "$*"; }
# run 'CMD': print the command exactly as written, then run it.
run() { printf '\n$ %s\n' "$1"; eval "$1"; }
# expect CODE 'CMD': run a command that must exit with CODE.
expect() {
  local code=0
  printf '\n$ %s\n' "$2"
  eval "$2" || code=$?
  printf '[exit %s]\n' "$code"
  [ "$code" = "$1" ] || { echo "demo: expected exit $1" >&2; exit 1; }
}
change() {
  printf '\n$ diff -u %s %s\n' "$1" "$2"
  diff -u -L "$1" -L "$2" "../examples/$1" "../examples/$2" || true
  run "cp ../examples/$2 main.wb.hcl"
}

case_ "1. Initial plan and apply in a fresh disposable directory"
note "Source: two resources and one task. The task's stdin references file.input.path."
run "cp ../examples/v1.wb.hcl main.wb.hcl"
run "cat main.wb.hcl"
note "Normalized intent: nodes in dependency order, evaluated attributes, owned and read paths."
run "wb compile"
note "Proposed changes. Planning observes the workspace and writes nothing but the plan file."
run "wb plan -out ../plan.json"
note "Apply the reviewed plan, then observe the outcome afresh."
run "wb apply -plan ../plan.json"
run "cat output.txt"

case_ "2. Unchanged re-apply"
run "wb apply"
note "Evidence: one line per effect attempt. The re-apply added nothing."
run "wb log"

case_ "3. Input change and the resulting downstream plan"
change v1.wb.hcl v2.wb.hcl
run "wb plan -out ../plan.json"
run "wb apply -plan ../plan.json"

case_ "4. External modification between plan and apply"
change v2.wb.hcl v3.wb.hcl
run "wb plan -out ../plan.json"
note "After review, someone edits input.txt by hand."
run 'echo "edited by hand" >> input.txt'
expect 3 "wb apply -plan ../plan.json"
note "A fresh plan shows exactly what applying would overwrite. Review it, then apply that plan."
run "wb plan -out ../plan.json"
run "wb apply -plan ../plan.json"

case_ "5. Injected partial failure, then retry"
change v3.wb.hcl v4.wb.hcl
expect 1 "WB_FAULT=exec.shout wb apply"
note "Retry: only the failed task is left. Completed effects are not repeated."
run "wb plan -out ../plan.json"
run "wb apply -plan ../plan.json"
run "wb log"

case_ "6. Third adapter (dir) with no parser or planner edits"
note "The extension is one adapter file plus one registration line:"
(cd "$repo" && run "wc -l adapters/dir.go")
(cd "$repo" && run "grep -n 'Dir{}' adapters/builtin.go")
note "No engine file (internal/: compiler, planner, executor) imports the builtin adapters:"
(cd "$repo" && run "grep -rl --include='*.go' --exclude='*_test.go' 'language-bakeoff/entries/hcl/adapters\"' internal/ || echo none")
change v4.wb.hcl v5.wb.hcl
run "wb plan -out ../plan.json"
run "wb apply -plan ../plan.json"
run "cat build/output.txt"
run "wb apply"

printf '\n\nDemo complete: six cases in a disposable directory.\n'
