#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")" && pwd)

for entry in hcl custom typed; do
  printf '\n\n========== %s entry ==========\n' "$entry"
  "$root/entries/$entry/demo.sh"
done

printf '\n\nAll language bakeoff demos passed.\n'
