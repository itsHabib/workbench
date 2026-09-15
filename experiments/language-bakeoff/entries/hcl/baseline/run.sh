#!/bin/sh
# Direct-tools baseline for the common workload: the same effects as
# examples/v1.wb.hcl, written as a plain POSIX script over data files plus
# one stamp file. No wb, no HCL, no journal.
#
# usage: baseline/run.sh <desired-dir> <workspace>
#   <desired-dir>/input.txt   desired content of the input artifact
#   <desired-dir>/notes.txt   desired content of the unrelated notes file
#
# Each step is guarded, so re-running it repeats nothing that already
# matches; writes are temp-then-rename; the transform re-runs only when the
# input or output differs from what its last successful run recorded.
set -eu

desired=$(cd "$1" && pwd)
cd "$2"
trap 'rm -f .input.txt.tmp .notes.txt.tmp .output.txt.tmp' EXIT

digest() {
  if [ ! -f "$1" ]; then echo absent; return; fi
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d ' ' -f 1; return; fi
  shasum -a 256 "$1" | cut -d ' ' -f 1
}

# sync NAME: make ./NAME match the desired copy, if it differs.
sync() {
  if cmp -s "$desired/$1" "$1"; then return 0; fi
  cp "$desired/$1" ".$1.tmp"
  mv ".$1.tmp" "$1"
  echo "updated $1"
}

sync input.txt

mkdir -p .baseline
stamp="$(digest input.txt) $(digest output.txt)"
if [ "$(cat .baseline/shout 2>/dev/null || true)" != "$stamp" ]; then
  LC_ALL=C tr a-z A-Z < input.txt > .output.txt.tmp
  mv .output.txt.tmp output.txt
  echo "$(digest input.txt) $(digest output.txt)" > .baseline/shout
  echo "ran shout"
fi

sync notes.txt
