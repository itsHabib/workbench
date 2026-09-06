#!/usr/bin/env bash
# Install the Go fleet binary behind the harness hooks on this machine, or roll it back.
#
#   bash cmd/fleet/install.sh            dry run: shows every change it would make
#   bash cmd/fleet/install.sh --apply    build, back up, rewrite hook commands, install lanes
#   bash cmd/fleet/install.sh --shadow   build, back up, ADD `fleet hook <h> --shadow` beside each
#                                        installed hook line; nothing else changes. A day later,
#                                        `fleet shadow-report` says whether to --apply.
#   bash cmd/fleet/install.sh --rollback restore the newest backups of the hook configs
#
# What --apply does, in order, each step said before it runs:
#   1. go build -> $FLEET_HOME/bin/fleet (default FLEET_HOME=~/.fleet)
#   2. copy the lane manifests+cards from $LANES_SRC (default: cc-skills
#      docs/features/agent-fleet-rules/lanes) to $FLEET_HOME/lanes, keeping a backup
#   3. back up ~/.claude/settings.json and ~/.codex/hooks.json beside themselves
#      (<name>.bak-<timestamp>) and rewrite the fleet hook commands:
#        python3 <FLEET_HOME>/hook.py           -> <FLEET_HOME>/bin/fleet hook claude
#        python3 <FLEET_HOME>/codex-adapter.py  -> <FLEET_HOME>/bin/fleet hook codex
#   4. run one SessionStart-free migration pass (fleet x-migrate) so keys are current
#
# Live sessions keep running: the Python hook and the Go hook read and write the same
# store with the same protocol, so a session that started under one finishes under the
# other. A version skew is loud, not silent. --rollback puts the Python hook back.
#
# This edits harness configuration. Run it yourself; an agent should not.
set -euo pipefail

mode="${1:-dry}"
FLEET_HOME="${FLEET_HOME:-$HOME/.fleet}"
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
LANES_SRC="${LANES_SRC:-$HOME/dev/cc-skills/docs/features/agent-fleet-rules/lanes}"
claude_settings="$HOME/.claude/settings.json"
codex_hooks="$HOME/.codex/hooks.json"
stamp="$(date +%Y%m%d-%H%M%S)"
bin="$FLEET_HOME/bin/fleet"

say() { printf '%s\n' "$*"; }
plan() { say "  [$mode] $*"; }

rollback() {
  for f in "$claude_settings" "$codex_hooks"; do
    latest="$(ls -1t "$f".bak-* 2>/dev/null | head -1 || true)"
    if [ -z "$latest" ]; then say "no backup for $f; nothing to restore"; continue; fi
    say "restore $latest -> $f"
    cp "$latest" "$f"
  done
  say "rolled back; the Python hook is in force again. The Go binary stays at $bin (harmless, not wired)."
}

if [ "$mode" = "--rollback" ]; then rollback; exit 0; fi
if [ "$mode" != "--apply" ] && [ "$mode" != "--shadow" ] && [ "$mode" != "dry" ]; then say "usage: install.sh [--apply|--shadow|--rollback]"; exit 2; fi

say "fleet install ($mode) — FLEET_HOME=$FLEET_HOME"
plan "go build -o $bin ./cmd/fleet   (from $root)"
if [ "$mode" = "--apply" ] || [ "$mode" = "--shadow" ]; then
  mkdir -p "$FLEET_HOME/bin"
  (cd "$root" && go build -o "$bin" ./cmd/fleet)
fi

# Shadow: add the Go hook beside every installed fleet hook line and stop there.
shadow_add() {
  f="$1"; harness="$2"
  [ -f "$f" ] || { say "  $f absent; skipped"; return; }
  n="$({ grep -o "\"[^\"]*/\(hook\|codex-adapter\)\.py\"" "$f" || true; } | wc -l | tr -d ' ')"
  plan "$f: add '$bin hook $harness --shadow' beside $n fleet hook line(s) (backup $f.bak-$stamp)"
  if [ "$mode" = "--shadow" ]; then
    cp "$f" "$f.bak-$stamp"
    python3 - "$f" "$bin" "$harness" <<'PY'
import sys, json, io
f, bin, harness = sys.argv[1:4]
d = json.load(io.open(f, encoding="utf-8"))
cmd = "%s hook %s --shadow" % (bin, harness)
added = 0
for ev, groups in (d.get("hooks") or {}).items():
    for g in groups:
        hooks = g.get("hooks") or []
        if any(h.get("command", "").endswith(("/hook.py", "/codex-adapter.py")) for h in hooks) and not any(h.get("command") == cmd for h in hooks):
            hooks.append({"type": "command", "command": cmd, **({"statusMessage": "fleet shadow"} if harness == "codex" else {})})
            g["hooks"] = hooks; added += 1
io.open(f, "w", encoding="utf-8").write(json.dumps(d, indent=2) + "\n")
print("  added to %d event group(s)" % added)
PY
  fi
}
if [ "$mode" = "--shadow" ] || [ "$mode" = "dry" ]; then
  shadow_add "$claude_settings" claude
  shadow_add "$codex_hooks" codex
fi
if [ "$mode" = "--shadow" ]; then
  say "shadow installed. Nothing in the store changes; $FLEET_HOME/shadow.jsonl fills. Tomorrow: '$bin shadow-report --since 24h'. Undo: bash $here/install.sh --rollback"
  exit 0
fi

if [ -d "$LANES_SRC" ]; then
  plan "lanes: $LANES_SRC -> $FLEET_HOME/lanes (backup $FLEET_HOME/lanes.bak-$stamp)"
  if [ "$mode" = "--apply" ]; then
    [ -d "$FLEET_HOME/lanes" ] && cp -R "$FLEET_HOME/lanes" "$FLEET_HOME/lanes.bak-$stamp"
    mkdir -p "$FLEET_HOME/lanes"
    cp -R "$LANES_SRC"/. "$FLEET_HOME/lanes"/
  fi
else
  say "  lanes source $LANES_SRC not found; lanes left as they are"
fi

rewrite() {
  f="$1"
  [ -f "$f" ] || { say "  $f absent; skipped"; return; }
  # Any command that runs hook.py or codex-adapter.py, wherever it lives today (the
  # Codex hooks on one machine pointed at a copy in a session's scratchpad).
  n_claude="$({ grep -o '"[^"]*/hook\.py"' "$f" || true; } | wc -l | tr -d ' ')"
  n_codex="$({ grep -o '"[^"]*/codex-adapter\.py"' "$f" || true; } | wc -l | tr -d ' ')"
  plan "$f: $n_claude claude hook line(s), $n_codex codex hook line(s) -> $bin hook claude|codex (backup $f.bak-$stamp)"
  if [ "$mode" = "--apply" ]; then
    cp "$f" "$f.bak-$stamp"
    python3 - "$f" "$bin" <<'PY'
import sys, io, re
f, bin = sys.argv[1:3]
s = io.open(f, encoding="utf-8").read()
s = re.sub(r'"[^"]*/hook\.py"', '"%s hook claude"' % bin, s)
s = re.sub(r'"[^"]*/codex-adapter\.py"', '"%s hook codex"' % bin, s)
io.open(f, "w", encoding="utf-8").write(s)
PY
  fi
}
rewrite "$claude_settings"
rewrite "$codex_hooks"

plan "$bin x-migrate   (re-key any legacy per-branch state; idempotent)"
if [ "$mode" = "--apply" ]; then "$bin" x-migrate >/dev/null 2>&1 || say "  migrate reported nothing to do or is not a verb in this build"; fi

if [ "$mode" = "dry" ]; then
  say "dry run only. Re-run with --apply to make these changes; --rollback restores the newest backups."
else
  say "installed. New sessions run $bin; live ones finish under the Python hook on the same store."
  say "check: '$bin board' and '$bin work'. Roll back with: bash $here/install.sh --rollback"
fi
