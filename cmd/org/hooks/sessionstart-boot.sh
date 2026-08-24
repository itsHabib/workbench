#!/usr/bin/env bash
# SessionStart hook: inject a role's boot index into a fresh session.
#
# The session's cwd is mapped to a role via $ORG_STATE/roles.map, one line per
# binding:
#
#   <path-prefix> <tenant> <role>
#   /Users/mh/dev/workbench mh lead:agentic-development
#
# Longest matching prefix wins. No mapping, no org binary, no chain — all exit
# 0 with no output: re-entry is an offer, never a gate on starting a session.
#
# Install (in ~/.claude/settings.json under hooks):
#   "SessionStart": [{"hooks": [{"type": "command",
#     "command": "ORG_STATE=\"$HOME/dev/org/state\" bash \"$HOME/dev/workbench/cmd/org/hooks/sessionstart-boot.sh\"",
#     "timeout": 5}]}]
set -euo pipefail

ORG_BIN="${ORG_BIN:-org}"
ORG_STATE="${ORG_STATE:-$HOME/dev/org/state}"
MAP="$ORG_STATE/roles.map"

command -v "$ORG_BIN" >/dev/null 2>&1 || exit 0
command -v jq >/dev/null 2>&1 || exit 0
[ -f "$MAP" ] || exit 0

input="$(cat)"
cwd="$(jq -r '.cwd // empty' <<<"$input")"
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

boot="$("$ORG_BIN" boot -state "$ORG_STATE" -tenant "$tenant" -role "$role" \
  -max-bytes "${ORG_BOOT_BUDGET:-2048}" 2>/dev/null)" || exit 0
[ -n "$boot" ] || exit 0

jq -n --arg ctx "$boot" \
  '{hookSpecificOutput: {hookEventName: "SessionStart", additionalContext: $ctx}}'
