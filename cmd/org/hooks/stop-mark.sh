#!/usr/bin/env bash
# Stop hook: append a mechanical mark to the role's chain when a session ends.
#
# A mark is the host's observation, not the model's claim: session id, turn
# count, transcript path. It is deliberately NOT a checkpoint — distillation
# needs a model, and a record the working agent is required to write is a verb
# wearing a costume. The fold renders a mark tip as Degraded, which is the
# honest state: activity happened and nobody has distilled it yet.
#
# Same roles.map contract as sessionstart-boot.sh; fail-open throughout.
#
# Install (in ~/.claude/settings.json under hooks):
#   "Stop": [{"hooks": [{"type": "command",
#     "command": "ORG_STATE=\"$HOME/dev/org/state\" bash \"$HOME/dev/workbench/cmd/org/hooks/stop-mark.sh\"",
#     "timeout": 10}]}]
set -euo pipefail

ORG_BIN="${ORG_BIN:-org}"
ORG_STATE="${ORG_STATE:-$HOME/dev/org/state}"
MAP="$ORG_STATE/roles.map"

command -v "$ORG_BIN" >/dev/null 2>&1 || exit 0
command -v jq >/dev/null 2>&1 || exit 0
[ -f "$MAP" ] || exit 0

input="$(cat)"
# Re-entrancy: a Stop hook that makes the agent continue re-fires Stop.
[ "$(jq -r '.stop_hook_active // false' <<<"$input" 2>/dev/null)" = "true" ] && exit 0
cwd="$(jq -r '.cwd // empty' <<<"$input" 2>/dev/null)" || exit 0
session="$(jq -r '.session_id // empty' <<<"$input" 2>/dev/null)" || exit 0
transcript="$(jq -r '.transcript_path // empty' <<<"$input" 2>/dev/null)" || exit 0
[ -n "$cwd" ] || exit 0

tenant="" role="" best=0
while read -r prefix map_tenant map_role; do
  case "$prefix" in ''|'#'*) continue ;; esac
  case "$cwd" in "$prefix"*) ;; *) continue ;; esac
  if [ "${#prefix}" -gt "$best" ]; then
    best="${#prefix}" tenant="$map_tenant" role="$map_role"
  fi
done <"$MAP"
[ -n "$role" ] || exit 0

turns=""
if [ -n "$transcript" ] && [ -f "$transcript" ]; then
  turns="$(jq -rs '[.[] | select(.type == "assistant")] | length' "$transcript" 2>/dev/null || true)"
fi

body="session ${session:0:8} stopped in $cwd"
[ -n "$turns" ] && body="$body after $turns assistant turns"
body="$body; transcript $transcript"

printf '%s' "$body" | "$ORG_BIN" mark -state "$ORG_STATE" -tenant "$tenant" \
  -role "$role" -body - >/dev/null 2>&1 || exit 0
