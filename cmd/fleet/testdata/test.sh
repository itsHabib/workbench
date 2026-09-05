#!/usr/bin/env bash
# Simulates the harness events the fleet hook consumes, against a throwaway repo + worktree.
# Every scenario names the exit code it expects: 0 allow / 2 deny. Exits 1 if any scenario fails.
# This proves the policy, not the harness wiring — run one real session with the hook installed next.
#
# Windows / Git Bash: every path that crosses into Python is converted to native form with
# cygpath -m first. Git Bash converts a bare /tmp/x argument itself, but not one embedded in a
# JSON string, and a Windows Python resolves /tmp/x somewhere else entirely.
set -u
here="$(cd "$(dirname "$0")" && pwd)"
native() { cygpath -m "$1" 2>/dev/null || printf '%s' "$1"; }
work="$(mktemp -d)"
export FLEET_STATE="$(native "$work/state")" ORG_STATE="$(native "$work/org")" CODEX_HOME="$(native "$work/codex-home")"
mkdir -p "$FLEET_STATE" "$ORG_STATE" "$CODEX_HOME"
cd "$work" || exit 1
git init -q repo && git -C repo -c user.email=t@t -c user.name=t commit -q --allow-empty -m init \
  && git -C repo checkout -q -b feat/x && git -C repo worktree add -q "$work/wt" -b feat/y || exit 1
REPO="$(native "$work/repo")"; WT="$(native "$work/wt")"; WORK="$(native "$work")"
printf '%s work supervisor:cam\n%s work finisher:cam\n' "$REPO" "$WT" > "$ORG_STATE/roles.map"
cp "$here/expensive.example.json" "$FLEET_STATE/expensive.json"
H="${FLEET_HOOK:-$here/hook.py}"; F="$here/fleet.py"
# Lane manifests: resolved the way fleet.py resolves them — $FLEET_LANES, else lanes/ beside this
# script (the installed layout, ~/.fleet/lanes), else ../lanes (the repo layout, beside ref/).
LANES="${FLEET_LANES:-}"; [ -n "$LANES" ] || { [ -d "$here/lanes" ] && LANES="$here/lanes" || LANES="$here/../lanes"; }
[ -d "$LANES" ] || { echo "no lanes directory at $LANES (set FLEET_LANES or install lanes/ beside test.sh)"; exit 1; }
# Phase 1 scenarios need a lane that requires a resource and produces a receipt kind. The suite
# works on a copy of lanes/ plus that one extra lane, so the shipped manifests stay untouched.
cp -r "$LANES" "$work/lanes" && mkdir -p "$work/lanes/liverun" \
  && printf '{"kind":"liverun","card":"card.md","denies":["Bash(gh pr merge:*)"],"requires":["slot:hyper"],"produces":"live","slots":1}\n' > "$work/lanes/liverun/manifest.json" \
  && printf 'You are the live-run lane. Hold the slot, run the packet, record the receipt, drop the slot.\n' > "$work/lanes/liverun/card.md"
export FLEET_LANES="$(native "$work/lanes")"; LANES="$FLEET_LANES"
export FLEET_HOOK_DIR="$(native "$here")"   # scenarios that import xlib as hook.py directly rather than firing an event
PY="${FLEET_PY:-python3}"
command -v "$PY" >/dev/null 2>&1 || PY=python
S1=local_1111aaaa S2=local_2222bbbb S3=local_3333cccc

# The harness does not spawn hooks through this shell. An invocation that resolves in Git Bash
# can still be a shell function or a PATH entry only this shell has, so the one we report is the
# one we saw resolve outside it too. Reported, never guessed: it goes into the six matchers verbatim.
hook_invocation() {
  local native_ok=n
  case "$(uname -s 2>/dev/null)" in
    MINGW*|MSYS*|CYGWIN*)
      cmd.exe //c "$PY -V" >/dev/null 2>&1 && native_ok=y ;;
    *) command -v env >/dev/null 2>&1 && env "$PY" -V >/dev/null 2>&1 && native_ok=y ;;
  esac
  printf '%s' "$PY"
  [ "$native_ok" = y ] || printf ' (WARNING: resolves in this shell only — verify before writing it into a matcher)'
}

# Which liveness path this box gets, because the two mean different things to `fleet leases`.
liveness_path() {
  if "$PY" -c 'import psutil' 2>/dev/null; then
    printf 'psutil present — the hook can verify a harness pid, so a dead holder is spotted at once (a record still reads parent-unverified when no claude/node/electron ancestor is found, e.g. under this script)'
    return
  fi
  case "$(uname -s 2>/dev/null)" in
    MINGW*|MSYS*|CYGWIN*|Windows*) printf 'staleness only (no psutil, and the ps walk is POSIX-only) — every record is parent-unverified and a dead holder reads alive for FLEET_STALE_S=%ss; `pip install psutil` to fix' "${FLEET_STALE_S:-7200}" ;;
    *) printf 'ps walk (no psutil) — SessionStart walks `ps -o ppid=,comm=` to the harness pid and records harness; a session whose ancestry has no claude/codex/node process stays parent-unverified and reads alive for FLEET_STALE_S=%ss. psutil is optional here' "${FLEET_STALE_S:-7200}" ;;
  esac
}

fails=0
ev() { "$PY" -c 'import json,sys; print(json.dumps(dict(a.split("=",1) for a in sys.argv[1:])))' "$@"; }
tool() { "$PY" -c "import json,os,sys; tool=sys.argv[4]; inp=json.loads(sys.argv[6]); codex=os.environ.get('FLEET_CODEX_EVENTS') and tool in ('Edit','Write','MultiEdit'); path=inp.get('file_path',''); tool='apply_patch' if codex else tool; inp={'command':f'*** Begin Patch\n*** Update File: {path}\n*** End Patch'} if codex else inp; print(json.dumps({'hook_event_name':sys.argv[1],'session_id':sys.argv[2],'cwd':sys.argv[3],'tool_name':tool,'tool_use_id':sys.argv[5],'tool_input':inp,'tool_response':{}}))" "$@"; }
multi_patch() { "$PY" -c "import json,sys; print(json.dumps({'hook_event_name':'PreToolUse','session_id':sys.argv[1],'cwd':sys.argv[2],'tool_name':'apply_patch','tool_use_id':'multi','tool_input':{'command':f'*** Begin Patch\n*** Update File: {sys.argv[3]}\n*** Move to: {sys.argv[4]}\n*** End Patch'}}))" "$@"; }
run() { # run <label> <want-exit> <event-json>
  local out rc; out=$(printf '%s' "$3" | "$PY" "$H" 2>"$work/err"); rc=$?
  if [ "$rc" = "$2" ]; then printf '  ok    %s\n' "$1"; else printf '  FAIL  %s (exit %s, wanted %s)\n' "$1" "$rc" "$2"; fails=$((fails+1)); fi
  [ -n "${VERBOSE:-}" ] && { [ -n "$out" ] && echo "        $out"; [ -s "$work/err" ] && echo "        $(cat "$work/err")"; }
  return 0
}
age() { "$PY" - "$1" "$2" "$3" <<'PY'
import json,sys; p,k,d=sys.argv[1],sys.argv[2],float(sys.argv[3]); r=json.load(open(p)); r[k]-=d; json.dump(r,open(p,'w'))
PY
}
ignored() { local repo="$1"; shift; for p in "$@"; do git -C "$repo" check-ignore -q "$p" || return 1; done; }
echo "fleet hook scenarios in $work"
echo "python invocation for the six matchers: $(hook_invocation)"
echo "liveness path on this box: $(liveness_path)"
run "SessionStart: finisher in the worktree, role + branch resolved"        0 "$(ev hook_event_name=SessionStart session_id=$S1 cwd=$WT source=startup)"
# Defect 10: liveness degraded silently. The two bases mean different things to `fleet leases` —
# `harness` is a real pid, so a dead holder is spotted at once; `parent-unverified` falls back to
# staleness and a dead holder reads alive for FLEET_STALE_S. The invariant that holds everywhere:
# the basis is always recorded and named, and `harness` is unreachable without psutil. (Under this
# script the ancestor is bash, not claude, so parent-unverified is the correct answer even with
# psutil installed — that is the honest signal, not the defect.)
got_kind=$("$PY" -c 'import json,sys; print(json.load(open(sys.argv[1])).get("pid_kind"))' "$FLEET_STATE/sessions/$S1.json" 2>/dev/null)
case "$got_kind" in
  # `harness` is legitimate without psutil since the POSIX ps walk (defect 10, Mac side), but only
  # if the pid it names is real and alive right now.
  harness) "$PY" -c 'import json,os,sys; r=json.load(open(sys.argv[1])); os.kill(int(r["pid"]), 0)' "$FLEET_STATE/sessions/$S1.json" 2>/dev/null && echo "  ok    liveness basis recorded as harness, and the pid it names is alive (psutil or ps walk)" || { echo "  FAIL  claimed a harness pid that is not alive"; fails=$((fails+1)); };;
  parent-unverified) echo "  ok    liveness basis recorded as parent-unverified (staleness fallback, stated not silent)";;
  *) echo "  FAIL  session record names no liveness basis (pid_kind=$got_kind)"; fails=$((fails+1));;
esac
run "Edit by finisher claims the feat/y lease on first write"               0 "$(tool PreToolUse $S1 $WT Edit t1 "{\"file_path\":\"$WT/a.ts\"}")"
# The lease filename is <repo-id>__<branch> since defect 12, so match on the suffix, not a fixed name.
lease1=$(ls "$FLEET_STATE/leases"/*__feat__y.json 2>/dev/null | head -1)
[ -n "$lease1" ] && grep -q "$S1" "$lease1" && echo "  ok    lease file written for feat/y by $S1 ($(basename "$lease1"))" || { echo "  FAIL  no lease file after the first write (branch detection or state path is broken on this box)"; fails=$((fails+1)); }
run "SessionStart: supervisor in the main checkout"                          0 "$(ev hook_event_name=SessionStart session_id=$S2 cwd=$REPO source=startup)"
[ -z "${FLEET_CODEX_EVENTS:-}" ] || run "Codex multi-file patch checks the later held path → denied" 2 "$(multi_patch $S2 $REPO $REPO/free.ts $WT/held.ts)"
# The lease file is <repo-id>__feat__x.json (defect 12). The old assertion named `feat__x.json`, a
# file that can no longer exist, so it passed with the rollback dead and the false lease present.
[ -z "${FLEET_CODEX_EVENTS:-}" ] || { [ -z "$(ls "$FLEET_STATE/leases"/*__feat__x.json 2>/dev/null)" ] && echo "  ok    denied Codex patch leaves no false lease on its earlier path" || { echo "  FAIL  denied Codex patch left a false lease: $(ls "$FLEET_STATE/leases")"; fails=$((fails+1)); }; }
run "Supervisor edits a file inside the held worktree → denied"             2 "$(tool PreToolUse $S2 $REPO Edit t2 "{\"file_path\":\"$WT/a.ts\"}")"
run "Supervisor 'git status' on its own branch → allowed (not a write)"     0 "$(tool PreToolUse $S2 $REPO Bash t3 '{"command":"git status"}')"
run "Supervisor 'git push' from the held worktree → denied"                 2 "$(tool PreToolUse $S2 $WT Bash t4 '{"command":"git push -u origin feat/y"}')"
run "Supervisor 'git -C <worktree> push' from its own cwd → denied (target, not cwd)" 2 "$(tool PreToolUse $S2 $REPO Bash t4b "{\"command\":\"git -C $WT push\"}")"
run "Supervisor 'cd <worktree> && git commit' from its own cwd → denied"      2 "$(tool PreToolUse $S2 $REPO Bash t4c "{\"command\":\"cd $WT && git commit -m x\"}")"
# Finding 6: a git write is a git SUBCOMMAND at the command position, not a word anywhere after `git`.
run "'git log -- src/apply.ts' on the held branch is a read → allowed"       0 "$(tool PreToolUse $S2 $WT Bash t4d '{"command":"git log --oneline -- src/apply.ts"}')"
run "'git branch -D' on the held branch is a write → denied"                 2 "$(tool PreToolUse $S2 $WT Bash t4e '{"command":"git branch -D feat/y"}')"
run "'git update-ref' on the held branch is a write → denied"                2 "$(tool PreToolUse $S2 $WT Bash t4f '{"command":"git update-ref refs/heads/feat/y HEAD~1"}')"
run "an echoed 'git -C <worktree> push' names no target and is no write → allowed" 0 "$(tool PreToolUse $S2 $REPO Bash t4g "{\"command\":\"echo \\\"git -C $WT push\\\"\"}")"
(cd "$work/wt" && "$PY" "$F" revoke feat/y --to local_2222 "supervisor taking the fix") >/dev/null
# The flag is addressed to the displaced holder (S1). A bystander refused by it (S3, never the holder)
# must not consume the signal: after S3's denial the flag still stands for S1.
run "After revoke: a bystander's call on feat/y is also stood down"          2 "$(tool PreToolUse $S3 $WT Bash t5z '{"command":"git status"}')"
[ -n "$(ls "$FLEET_STATE/stop"/*__feat__y.json 2>/dev/null)" ] && echo "  ok    a bystander's denial does not retire the flag meant for the displaced holder" || { echo "  FAIL  bystander consumed the revoke flag"; fails=$((fails+1)); }
run "After revoke: finisher's next call on feat/y → STAND DOWN"             2 "$(tool PreToolUse $S1 $WT Bash t5 '{"command":"git status"}')"
# Finding 2 (Q2 ruling): a revoke's flag is a signal delivered once; the lease is the authority.
# Retire path (a): the old holder has been denied by it. From here the new holder's lease keeps the
# old one out, with the better refusal text. Path (b), the except session ending, is tested below.
[ -z "$(ls "$FLEET_STATE/stop"/*__feat__y.json 2>/dev/null)" ] && echo "  ok    a revoke's flag retires once its stand-down has been delivered" || { echo "  FAIL  revoke flag still standing after it refused the old holder"; fails=$((fails+1)); }
err=$(printf '%s' "$(tool PreToolUse $S1 $WT Edit t5b "{\"file_path\":\"$WT/a.ts\"}")" | "$PY" "$H" 2>&1 >/dev/null); rc=$?
case "$rc:$err" in 2:*"held by"*) echo "  ok    the old holder's next write is refused by the lease, not by a stale flag";; *) echo "  FAIL  old holder after revoke: rc=$rc $err"; fails=$((fails+1));; esac
out=$(printf '%s' "$(ev hook_event_name=UserPromptSubmit session_id=$S1 cwd=$WT prompt=hi)" | "$PY" "$H")
case "$out" in *"now held by"*) echo "  ok    the old holder's next turn is told the branch moved, not that it may act again";; *) echo "  FAIL  resume line after a revoke: $out"; fails=$((fails+1));; esac
run "After revoke: supervisor edits in the worktree → allowed"              0 "$(tool PreToolUse $S2 $REPO Edit t6 "{\"file_path\":\"$WT/a.ts\"}")"
run "Unfiltered vitest → denied with measured cost + targeted alternative"  2 "$(tool PreToolUse $S2 $REPO Bash t7 '{"command":"npx vitest run"}')"
# Open 2: cost rules match the command's EXECUTION POSITION, not the whole string. Matching the
# whole string produced the same false positive three times (an `org checkpoint` whose body text
# named the runner, a `grep` searching for it, a `gh pr comment` quoting the denial), and rewording
# was the workaround each time. `unless` stays on the full command: the exemptions are arguments.
run "a command that only MENTIONS the suite runs (grep)"                     0 "$(tool PreToolUse $S2 $REPO Bash t7a '{"command":"grep -rniE vitest hook.py"}')"
run "a commit message naming the suite runs"                                 0 "$(tool PreToolUse $S2 $REPO Bash t7b '{"command":"git commit -m \"ran pnpm test locally\""}')"
run "turbo run test is still denied (head keeps 3 words, signature() would not)" 2 "$(tool PreToolUse $S2 $REPO Bash t7c '{"command":"turbo run test --env-mode=loose"}')"
run "pnpm test --filter=<pkg> is targeted → allowed"                         0 "$(tool PreToolUse $S2 $REPO Bash t7d '{"command":"pnpm test --filter=@scope/pkg"}')"
# A rule with no `pattern`/`instead`/`seconds` used to KeyError into the fail-open catch, running
# the suite with NO lock and NO ledger row — the opposite of the rule's intent, silently.
cp "$FLEET_STATE/expensive.json" "$work/expensive.keep"
"$PY" -c "import json,sys; rules=json.load(open(sys.argv[1])); rules.insert(0, {'name':'malformed'}); json.dump(rules, open(sys.argv[1],'w'))" "$FLEET_STATE/expensive.json"
run "a malformed rule is skipped, and the real rule still denies"            2 "$(tool PreToolUse $S2 $REPO Bash t7e '{"command":"npx vitest run"}')"
cp "$work/expensive.keep" "$FLEET_STATE/expensive.json"
run "vitest on one test file → allowed"                                     0 "$(tool PreToolUse $S2 $REPO Bash t8 '{"command":"npx vitest run src/a.test.ts"}')"
run "Whole-project tsc → denied"                                            2 "$(tool PreToolUse $S2 $REPO Bash t9 '{"command":"npx tsc --noEmit"}')"
run "tsc -p <package> → allowed"                                            0 "$(tool PreToolUse $S2 $REPO Bash t10 '{"command":"npx tsc --noEmit -p apps/web"}')"
run "FLEET_ALLOW_SLOW override → allowed, logged, suite lock taken"         0 "$(tool PreToolUse $S2 $REPO Bash t11 '{"command":"FLEET_ALLOW_SLOW=\"wire contract\" npx vitest run"}')"
run "Second session starts the same suite while it runs → denied (lock)"    2 "$(tool PreToolUse $S3 $REPO Bash t12 '{"command":"FLEET_ALLOW_SLOW=x npx vitest run"}')"
age "$FLEET_STATE/inflight/t11.json" at 1403
run "PostToolUse: elapsed lands in the ledger, lock released"               0 "$(tool PostToolUse $S2 $REPO Bash t11 '{"command":"FLEET_ALLOW_SLOW=\"wire contract\" npx vitest run"}')"
# The row's seconds is 1403 of back-dating plus however long this box really took between the two
# events — ~2s of interpreter startup on Windows. Assert the signature and the order of magnitude,
# never the exact number: a slower machine is not a policy failure.
"$PY" - "$FLEET_STATE/costs.jsonl" <<'PY' && echo "  ok    ledger row: npx vitest ~1403s" || { echo "  FAIL  ledger row missing or wrong"; fails=$((fails+1)); }
import json,sys
rows=[json.loads(l) for l in open(sys.argv[1],encoding="utf-8")]
sys.exit(0 if any(r["sig"]=="npx vitest" and 1403 <= r["seconds"] < 1463 for r in rows) else 1)
PY
run "Same suite after the lock cleared → allowed"                           0 "$(tool PreToolUse $S3 $REPO Bash t13 '{"command":"FLEET_ALLOW_SLOW=x npx vitest run"}')"
run "Stop: turn closes"                                                     0 "$(ev hook_event_name=Stop session_id=$S2 cwd=$REPO)"
age "$FLEET_STATE/sessions/$S2.json" last_stop_at 2900
run "UserPromptSubmit: the gap since the last turn is injected"             0 "$(ev hook_event_name=UserPromptSubmit session_id=$S2 cwd=$REPO prompt=hi)"
run "SessionStart on resume: stop flag, foreign lease, cost table injected" 0 "$(ev hook_event_name=SessionStart session_id=$S1 cwd=$WT source=resume)"
"$PY" "$F" decide drop "pr#4610" "operator dropped it; do not resurface" >/dev/null
"$PY" "$F" decide ignore "field:internal_status" "useless; never investigate it" >/dev/null
out=$(printf '%s' "$(ev hook_event_name=UserPromptSubmit session_id=$S3 cwd=$REPO prompt=hi)" | "$PY" "$H")
case "$out" in *"d1 drop pr#4610"*"d2 ignore field:internal_status"*) echo "  ok    UserPromptSubmit: operator decisions reach a session that never heard them";; *) echo "  FAIL  decisions not injected: $out"; fails=$((fails+1));; esac
"$PY" "$F" undecide d2 >/dev/null
out=$(printf '%s' "$(ev hook_event_name=SessionStart session_id=$S3 cwd=$REPO source=resume)" | "$PY" "$H")
case "$out" in *"d1 drop"*) case "$out" in *"d2 ignore"*) echo "  FAIL  retired decision still injected"; fails=$((fails+1));; *) echo "  ok    SessionStart: a retired decision is gone, the live one stays";; esac;; *) echo "  FAIL  decisions missing at SessionStart: $out"; fails=$((fails+1));; esac
run "SessionEnd releases the supervisor's lease"                            0 "$(ev hook_event_name=SessionEnd session_id=$S2 cwd=$REPO reason=exit)"
# No manual `fleet resume feat/y` here: it masked finding 2. The revoke's flag is gone on its own.
run "New session claims the released branch"                                0 "$(tool PreToolUse $S3 $WT Edit t14 "{\"file_path\":\"$WT/a.ts\"}")"
"$PY" - "$FLEET_STATE/sessions/$S3.json" <<'PY'
import json,sys; p=sys.argv[1]; r=json.load(open(p)); r['pid']=999999; r['pid_kind']='harness'; json.dump(r,open(p,'w'))
PY
run "Holder's harness pid is dead → lease taken over, not denied"          0 "$(tool PreToolUse $S1 $WT Edit t15 "{\"file_path\":\"$WT/a.ts\"}")"
run "Detached / no repo: no branch, no lease, allowed"                      0 "$(tool PreToolUse $S3 $WORK Bash t16 '{"command":"git push"}')"
run "Garbage on stdin → fail-open (exit 0, error logged)"                   0 "not json"
# Defect 8: tier.json was not on the install list, so `fleet tier` could not answer from the first
# session. Two halves: the missing file must name itself, and the starter the install step prescribes
# (the example with `critical` and `wire` emptied, before the operator signs the real lists) must run.
rm -f "$FLEET_STATE/tier.json"
out=$( (cd "$work/wt" && "$PY" "$F" tier --base feat/x) 2>&1 )
case "$out" in *tier.json*) echo "  ok    fleet tier without tier.json names the file it needs";; *) echo "  FAIL  missing tier.json did not name itself: $out"; fails=$((fails+1));; esac
"$PY" -c "import json,sys; c=json.load(open(sys.argv[1])); c['critical']=[]; c['wire']=[]; json.dump(c,open(sys.argv[2],'w'))" "$here/tier.example.json" "$FLEET_STATE/tier.json"
out=$( (cd "$work/wt" && "$PY" "$F" tier --base feat/x --json) 2>&1 )
case "$out" in *'"tier": 0'*) echo "  ok    the step-1 starter tier.json (critical/wire empty) lets the verb answer";; *) echo "  FAIL  starter tier.json did not answer: $out"; fails=$((fails+1));; esac
cp "$here/tier.example.json" "$FLEET_STATE/tier.json"
mkdir -p "$work/wt/apps/web/src/api" "$work/wt/apps/desktop/src" "$work/wt/docs"
printf 'export function config(){ const degraded = false; return { webDisplay: false }; }\n' > "$work/wt/apps/web/src/api/cam_firewall.ts"
printf 'notes\n' > "$work/wt/docs/notes.md"
git -C "$work/wt" add -A && git -C "$work/wt" -c user.email=t@t -c user.name=t commit -qm "flag read"
tier=$(cd "$work/wt" && "$PY" "$F" tier --base feat/x --json | "$PY" -c 'import json,sys; print(json.load(sys.stdin)["tier"])')
[ "$tier" = "3" ] && echo "  ok    fleet tier: flag read on a critical path with a fail-mode default → T3" || { echo "  FAIL  tier=$tier wanted 3"; fails=$((fails+1)); }
git -C "$work/wt" rm -q --cached apps/web/src/api/cam_firewall.ts && rm "$work/wt/apps/web/src/api/cam_firewall.ts" && git -C "$work/wt" -c user.email=t@t -c user.name=t commit -qam "docs only"
tier=$(cd "$work/wt" && "$PY" "$F" tier --base feat/x --json | "$PY" -c 'import json,sys; print(json.load(sys.stdin)["tier"])')
[ "$tier" = "0" ] && echo "  ok    fleet tier: docs-only diff → T0" || { echo "  FAIL  tier=$tier wanted 0"; fails=$((fails+1)); }
# Defect 21: `fleet tier` decoded git's output with the locale encoding, so a diff containing any
# byte outside cp1252 killed the verb on Windows — and tier gates the merge guard, so the verb being
# unavailable is a merge that cannot be authorised. Commit a non-cp1252 path AND content.
# The filename is built with printf: bash does not expand \x escapes inside a redirect target, so the
# old form created a file literally named `na\xc3\xafve.md` (backslashes and all) on POSIX, which git
# quoted and no tier rule matched — the scenario tested the wrong thing and failed on every Mac. On
# Windows a backslash in a filename is refused and the fallback ran, which is why it passed there.
naive="$work/wt/docs/$(printf 'na\303\257ve.md')"
printf 'export const x = "\xe2\x80\x9dcurly\xe2\x80\x9d";\n' > "$naive" 2>/dev/null || printf 'x\n' > "$work/wt/docs/naive-fallback.md"
printf 'a line with a cp1252-hostile byte: \xe2\x80\x9d\n' >> "$work/wt/docs/notes.md"
git -C "$work/wt" add -A && git -C "$work/wt" -c user.email=t@t -c user.name=t commit -qm "non-cp1252 bytes"
out=$( (cd "$work/wt" && "$PY" "$F" tier --base feat/x --json) 2>&1 )
case "$out" in
  *UnicodeDecodeError*) echo "  FAIL  fleet tier died decoding git output: $(printf '%s' "$out" | head -1)"; fails=$((fails+1));;
  *'"tier"'*) echo "  ok    fleet tier survives a diff with non-cp1252 bytes";;
  *) echo "  FAIL  fleet tier gave no tier on a non-cp1252 diff: $(printf '%s' "$out" | head -2)"; fails=$((fails+1));;
esac

printf 'x\n' > "$work/wt/mystery.bin" && git -C "$work/wt" add -A && git -C "$work/wt" -c user.email=t@t -c user.name=t commit -qm "mystery"
(cd "$work/wt" && "$PY" "$F" tier --base feat/x >/dev/null 2>&1) && { echo "  FAIL  unmatched file got a default tier"; fails=$((fails+1)); } || echo "  ok    fleet tier: an unclassified file is an error, never a default tier"
"$PY" "$F" ready abc123def "open project P, upload the program" "" >/dev/null 2>&1 && { echo "  FAIL  ready accepted a packet with no observable"; fails=$((fails+1)); } || echo "  ok    fleet ready: refuses a packet with no observable"
"$PY" "$F" ready abc123def "open project P, upload the program" "the upload gate blocks with 'checks could not complete'" >/dev/null && echo "  ok    fleet ready: prints a complete packet" || { echo "  FAIL  ready refused a complete packet"; fails=$((fails+1)); }
# Phase 1: a receipt names its kind and is bound to a lane; the old kind-less form is refused, and
# a receipt from a directory with no live session is refused. The real flow is tested below.
"$PY" "$F" receipt abc123def pass "gate blocked as expected" >/dev/null 2>&1 && { echo "  FAIL  a receipt without a kind was accepted"; fails=$((fails+1)); } || echo "  ok    fleet receipt: a receipt without a kind is refused"
[ ! -e "$FLEET_STATE/receipts/abc123def.live.json" ] && echo "  ok    nothing was written for the refused receipt" || { echo "  FAIL  refused receipt still wrote a file"; fails=$((fails+1)); }
(cd "$work/wt" && "$PY" "$F" handoff feat/y "degraded read fixed; contract test added" "stage the T3 negative run" >/dev/null)
out=$(printf '%s' "$(ev hook_event_name=SessionStart session_id=$S1 cwd=$WT source=resume)" | "$PY" "$H")
case "$out" in *"last handoff on feat/y"*"degraded read fixed"*"next: stage the T3"*) echo "  ok    fleet handoff: one line, injected at the next SessionStart on that branch";; *) echo "  FAIL  handoff not injected: $out"; fails=$((fails+1));; esac
"$PY" "$F" role "$WT" finisher:mono >/dev/null && grep -q "finisher:mono" "$ORG_STATE/roles.map" && grep -q "@" "$work/wt/CLAUDE.local.md" && "$PY" -c "import json,sys; d=json.load(open(sys.argv[1])); sys.exit(0 if 'Bash(gh pr merge:*)' in d['permissions']['deny'] else 1)" "$work/wt/.claude/settings.local.json" && echo "  ok    fleet role: Claude roles.map line, card import, and deny list" || { echo "  FAIL  fleet role Claude files"; fails=$((fails+1)); }
"$PY" -c "import sys,tomllib; d=tomllib.load(open(sys.argv[1],'rb')); text=d['developer_instructions']; sys.exit(0 if 'finisher:mono' in text and 'You are the finisher lane' in text else 1)" "$work/wt/.codex/config.toml" && [ ! -e "$work/wt/AGENTS.override.md" ] && echo "  ok    fleet role: Codex developer context carries the card without replacing AGENTS.md" || { echo "  FAIL  fleet role Codex context"; fails=$((fails+1)); }
"$PY" -c "import json,sys; d=json.load(open(sys.argv[1]))['hooks']; want={'SessionStart','UserPromptSubmit','PreToolUse','PostToolUse','Stop','SessionEnd'}; hs=[h for e in want for g in d[e] for h in g['hooks'] if h.get('statusMessage')=='fleet governance']; sys.exit(0 if set(d)>=want and len(hs)==len(want) and all(h['command'].endswith('codex-adapter.py') or h['command'].endswith(' hook codex') for h in hs) else 1)" "$CODEX_HOME/hooks.json" && [ ! -e "$work/wt/.codex/hooks.json" ] && echo "  ok    fleet role: all six user-level Codex hooks point to the adapter once" || { echo "  FAIL  fleet role Codex hooks"; fails=$((fails+1)); }
grep -q 'pattern = \["gh", "pr", "merge"\]' "$work/wt/.codex/rules/fleet-role.rules" && ignored "$WT" CLAUDE.local.md .codex/config.toml .codex/rules/fleet-role.rules && echo "  ok    fleet role: Codex merge rule and generated-file excludes are local" || { echo "  FAIL  fleet role Codex rule or excludes"; fails=$((fails+1)); }
"$PY" "$F" role "$WT" finisher:mono >/dev/null && [ "$(grep -c '^# BEGIN fleet role$' "$work/wt/.codex/config.toml")" = 1 ] && "$PY" -c "import json,sys; d=json.load(open(sys.argv[1]))['hooks']; sys.exit(0 if all(sum(h.get('statusMessage')=='fleet governance' for g in v for h in g['hooks'])==1 for v in d.values()) else 1)" "$CODEX_HOME/hooks.json" && echo "  ok    fleet role: Codex files are idempotent" || { echo "  FAIL  fleet role Codex idempotence"; fails=$((fails+1)); }
printf '"developer_instructions" = "keep me"\nmodel = "gpt-test"\n' > "$work/wt/.codex/config.toml"
cp "$work/wt/.codex/config.toml" "$work/quoted-config.before"
"$PY" "$F" role "$WT" finisher:mono >/dev/null 2>&1 && quoted_refused=0 || quoted_refused=1
[ "$quoted_refused" = 1 ] && cmp -s "$work/quoted-config.before" "$work/wt/.codex/config.toml" && "$PY" -c "import sys,tomllib; d=tomllib.load(open(sys.argv[1],'rb')); sys.exit(0 if d.get('developer_instructions')=='keep me' else 1)" "$work/wt/.codex/config.toml" && echo "  ok    fleet role: quoted developer_instructions is refused and preserved" || { echo "  FAIL  fleet role quoted TOML preservation"; fails=$((fails+1)); }
# Phase 0: Codex rules are derived from the manifest's denies, so the kind-specific git push rule
# (dropped from the denies by the defect 3 ruling but kept alive in a hand-written list) is gone too.
"$PY" "$F" role "$REPO" supervisor:cam >/dev/null && grep -q 'pattern = \["gh", "pr", "merge"\]' "$work/repo/.codex/rules/fleet-role.rules" && ! grep -q '"git", "push"' "$work/repo/.codex/rules/fleet-role.rules" && echo "  ok    fleet role: Codex rules are the manifest's denies, nothing more (no stale git push rule)" || { echo "  FAIL  Codex rules do not match the manifest's denies: $(cat "$work/repo/.codex/rules/fleet-role.rules")"; fails=$((fails+1)); }
# Phase 0 (TDD §4.1): a lane is a manifest plus a card under lanes/<kind>/; fleet role reads only the manifest.
out=$("$PY" "$F" role "$WT" nolane:mono 2>&1); rc=$?
case "$rc:$out" in 0:*) echo "  FAIL  a kind with no manifest was roled"; fails=$((fails+1));; *"lanes/nolane/manifest.json"*|*"nolane/manifest.json"*) echo "  ok    a kind with no manifest is refused, naming lanes/<kind>/manifest.json";; *) echo "  FAIL  refusal did not name the manifest path: $out"; fails=$((fails+1));; esac
out=$("$PY" "$F" role "$REPO" supervisor:cam 2>&1)   # $REPO, not $WT: the quoted-TOML scenario above leaves $WT refusing for its own reason
case "$out" in *"manifest "*"/supervisor/manifest.json"*) echo "  ok    fleet role reports the manifest it read";; *) echo "  FAIL  fleet role did not name its manifest: $out"; fails=$((fails+1));; esac
grep -q "lanes/supervisor/card.md" "$work/repo/CLAUDE.local.md" && echo "  ok    the card import points at lanes/<kind>/card.md" || { echo "  FAIL  card import: $(cat "$work/repo/CLAUDE.local.md")"; fails=$((fails+1)); }
mkdir -p "$work/lanes-bad/broken"; printf '{"kind":"broken","card":"card.md"}\n' > "$work/lanes-bad/broken/manifest.json"
out=$(FLEET_LANES="$work/lanes-bad" "$PY" "$F" role "$WT" broken:mono 2>&1); rc=$?
[ "$rc" != 0 ] && case "$out" in *"missing"*) echo "  ok    a manifest without the six keys is refused, naming what is missing";; *) echo "  FAIL  malformed manifest not refused clearly: $out"; fails=$((fails+1));; esac || { echo "  FAIL  malformed manifest accepted"; fails=$((fails+1)); }
# Six keys present but a wrong shape (`denies: null`) must be refused before any file is written.
mkdir -p "$work/lanes-bad/shaped"; printf '{"kind":"shaped","card":"card.md","denies":null,"requires":[],"produces":null,"slots":0}\n' > "$work/lanes-bad/shaped/manifest.json"; printf 'card\n' > "$work/lanes-bad/shaped/card.md"
cp "$ORG_STATE/roles.map" "$ORG_STATE/roles.map.shape"; rm -f "$work/repo/CLAUDE.local.md"
out=$(FLEET_LANES="$work/lanes-bad" "$PY" "$F" role "$REPO" shaped:cam 2>&1); rc=$?
[ "$rc" != 0 ] && cmp -s "$ORG_STATE/roles.map.shape" "$ORG_STATE/roles.map" && [ ! -e "$work/repo/CLAUDE.local.md" ] && case "$out" in *"denies must be a list"*) echo "  ok    a manifest with the right keys and a wrong shape is refused before any write";; *) echo "  FAIL  wrong-shape refusal unclear: $out"; fails=$((fails+1));; esac || { echo "  FAIL  wrong-shape manifest accepted or wrote files: rc=$rc"; fails=$((fails+1)); }
"$PY" "$F" role "$REPO" supervisor:cam >/dev/null 2>&1   # restore the checkout's role files for later scenarios
# FR1: the substrate contains no lane name and no domain word, comments included, any case, every
# lane kind. The one exemption is the command token `gh pr`: the GitHub CLI's own verb, which the
# substrate parses the way it parses `git`, is shell surface rather than domain vocabulary.
# The exemption removes only the `gh pr` token span from each line before the grep runs, so a domain
# word elsewhere on a line that also says `gh pr` is still caught (the old line-wide -v hid it).
hits=$(for f in "$here/hook.py" "$here/fleet.py" "$here/codex-adapter.py" "$here/fleet-mcp.py"; do sed -E 's/gh(\\s\+|\\s\*|[[:space:]]+)pr([^A-Za-z0-9_]|$)/gh_pull\2/g' "$f" | grep -niE '\b(pr|ci|review|hypermill|nx|finisher|author|liverun|supervisor|infra)\b' | sed "s#^#$(basename "$f"):#"; done || true)
[ -z "$hits" ] && echo "  ok    FR1: no lane name or domain word in hook.py, fleet.py, codex-adapter.py, fleet-mcp.py" || { echo "  FAIL  FR1: domain words in the substrate:"; printf '%s\n' "$hits" | sed 's/^/      /'; fails=$((fails+1)); }
[ -z "${FLEET_CODEX_EVENTS:-}" ] || { out=$(codex execpolicy check --rules "$work/wt/.codex/rules/fleet-role.rules" gh pr merge topic 2>/dev/null); printf '%s' "$out" | "$PY" -c "import json,sys; sys.exit(0 if json.load(sys.stdin).get('decision')=='forbidden' else 1)" && echo "  ok    Codex execpolicy loads the generated rule and forbids gh pr merge" || { echo "  FAIL  Codex execpolicy did not enforce the generated rule"; fails=$((fails+1)); }; }
# Defect 3: the harness loads <checkout>/.claude/settings.local.json for a worktree nested at
# <checkout>/.claude/worktrees/<x> as well, so a role's denies apply to every checkout beneath it.
# The supervisor's `Bash(git push:*)` therefore blocked its own finishers. Resolved by dropping that
# deny (the lease hook already refuses a write to a held branch, on evidence, with a redirect) —
# so what needs guarding now is the general rule: no role may deny an action its workers must take.
"$PY" - "$LANES" <<'PY' && echo "  ok    no lane deny blocks an action a nested worker must take" || { echo "  FAIL  a lane deny would be inherited by nested worktrees and block their work"; fails=$((fails+1)); }
import glob, json, os, sys
# A worker nested under a roled checkout inherits these denies. Anything here it legitimately does
# must therefore not be denied by ANY lane. push is the one that bit; commit/fetch are the same shape.
worker_must_do = ("git push", "git commit", "git fetch", "git rebase")
bad = []
for f in sorted(glob.glob(os.path.join(sys.argv[1], "*", "manifest.json"))):
    for rule in json.load(open(f, encoding="utf-8"))["denies"]:
        inner = rule[rule.find("(") + 1: rule.rfind(")")]
        for verb in worker_must_do:
            if inner.startswith(verb):
                bad.append((os.path.basename(f), rule, verb))
for f, rule, verb in bad:
    print("    %s denies %s -- a nested worker needs `%s`" % (f, rule, verb))
sys.exit(1 if bad else 0)
PY

# Defect 12: leases and stop flags were keyed by branch name alone, so `main` in cc-skills and `main`
# in workbench were ONE lease file and ONE stop file — stopping one stood the other down, and a write
# in one was refused because the other held "the" branch. Second repo, same branch name, and the
# identity has to survive a worktree (every worktree of a repo shares the commondir, so shares the key).
git init -q "$work/repo2" && git -C "$work/repo2" -c user.email=t@t -c user.name=t commit -q --allow-empty -m init && git -C "$work/repo2" checkout -q -b feat/y
REPO2="$(native "$work/repo2")"
printf '%s work finisher:other\n' "$REPO2" >> "$ORG_STATE/roles.map"
S4=local_4444dddd
run "repo2 has its own feat/y: a write there is allowed while repo1's feat/y is held" 0 "$(tool PreToolUse $S4 $REPO2 Edit t20 "{\"file_path\":\"$REPO2/a.ts\"}")"
n=$(ls "$FLEET_STATE/leases" | wc -l)
[ "$n" -ge 2 ] && echo "  ok    two same-named branches hold two separate lease files ($n leases)" || { echo "  FAIL  same-named branches in two repos share one lease file ($n leases)"; fails=$((fails+1)); }
(cd "$work/repo2" && "$PY" "$F" stop feat/y "stopping only repo2") >/dev/null
run "repo2's feat/y is stopped -> its next call is refused"                  2 "$(tool PreToolUse $S4 $REPO2 Bash t21 '{"command":"git status"}')"
run "repo1's feat/y is untouched by repo2's stop -> still allowed"           0 "$(tool PreToolUse $S3 $WT Bash t22 '{"command":"git status"}')"
(cd "$work/repo2" && "$PY" "$F" resume feat/y) >/dev/null
run "repo2 resumes independently"                                            0 "$(tool PreToolUse $S4 $REPO2 Bash t23 '{"command":"git status"}')"
out=$( (cd "$WORK" && "$PY" "$F" stop feat/y "from outside any repo") 2>&1 )
case "$out" in *"not inside a git repo"*) echo "  ok    a stop from outside any repo is refused, not applied to every repo";; *) echo "  FAIL  stop outside a repo was accepted: $out"; fails=$((fails+1));; esac

# Defect 17: a role is bound to a checkout, not inherited down a tree. A worktree nested under a
# supervisor checkout with no line of its own must resolve to NO role, not to the supervisor's —
# it is a worker, and the supervisor card's law is "never act on work". Tenancy still inherits.
cp "$ORG_STATE/roles.map" "$ORG_STATE/roles.map.d17"
printf '%s work supervisor:cam\n%s work finisher:cam\n' "$REPO" "$WT" > "$ORG_STATE/roles.map"
mkdir -p "$work/repo/.claude/worktrees/nested"
"$PY" - "$ORG_STATE" "$REPO" <<'PY' && echo "  ok    a nested worktree with no line of its own resolves to no role, not the supervisor's" || { echo "  FAIL  role inherited down the tree"; fails=$((fails+1)); }
import os, sys
os.environ["ORG_STATE"] = sys.argv[1]
sys.path.insert(0, os.environ["FLEET_HOOK_DIR"])
import xlib as hook
nested = sys.argv[2] + "/.claude/worktrees/nested"
role, tenant, _ = hook.map_rows_for(nested)
ok = True
if role is not None:
    print("    nested worktree got role %r; it has no line of its own" % role); ok = False
if tenant is None:
    print("    nested worktree lost its tenant; tenancy must still inherit by prefix"); ok = False
if hook.role_of(sys.argv[2]) is None:
    print("    the checkout itself lost its own exact-match role"); ok = False
sys.exit(0 if ok else 1)
PY
rmdir "$work/repo/.claude/worktrees/nested" 2>/dev/null

# Defect 18: the role was sticky for the life of a session, so a session that resolved the wrong
# role kept it even after the map was fixed. SessionStart re-resolves; between events it stays put.
S6=local_6666ffff
run "a session starts in the worktree as finisher:cam"                       0 "$(ev hook_event_name=SessionStart session_id=$S6 cwd=$WT source=startup)"
"$PY" -c "import json,sys; p=sys.argv[1]; r=json.load(open(p)); sys.exit(0 if r.get('role')=='finisher:cam' else 1)" "$FLEET_STATE/sessions/$S6.json" || { echo "  FAIL  session did not start as finisher:cam"; fails=$((fails+1)); }
swap_role() { "$PY" -c 'import sys; p,a,b=sys.argv[1:]; s=open(p).read().replace(a,b); open(p,"w",newline="").write(s)' "$ORG_STATE/roles.map" "$1" "$2"; }   # not sed -i: BSD sed needs an extension argument and silently edits nothing
swap_role "$WT work finisher:cam" "$WT work author:cam"
run "a mid-session event does NOT re-resolve the role"                       0 "$(tool PreToolUse $S6 $WT Bash t40 '{"command":"git status"}')"
"$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if r.get('role')=='finisher:cam' else 1)" "$FLEET_STATE/sessions/$S6.json" && echo "  ok    a map edit mid-turn does not change what a session is halfway through acting" || { echo "  FAIL  role changed mid-session"; fails=$((fails+1)); }
run "SessionStart on resume re-resolves the role"                            0 "$(ev hook_event_name=SessionStart session_id=$S6 cwd=$WT source=resume)"
"$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if r.get('role')=='author:cam' else 1)" "$FLEET_STATE/sessions/$S6.json" && echo "  ok    a corrected map reaches the session at its next SessionStart" || { echo "  FAIL  role stayed stale after SessionStart"; fails=$((fails+1)); }
# Finding 9: deleting the line (the operator's other correction) must strip the role at the next
# SessionStart rather than leave the session wearing the old card until the tab is closed.
swap_role "$WT work author:cam" ""
run "SessionStart with the line deleted"                                     0 "$(ev hook_event_name=SessionStart session_id=$S6 cwd=$WT source=resume)"
"$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if r.get('role') is None else 1)" "$FLEET_STATE/sessions/$S6.json" && echo "  ok    a deleted map line strips the role at SessionStart (no role, not a borrowed one)" || { echo "  FAIL  role survived its line being deleted: $(cat "$FLEET_STATE/sessions/$S6.json")"; fails=$((fails+1)); }
"$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if r.get('repo') and '__' not in r['repo'] else 1)" "$FLEET_STATE/sessions/$S6.json" && echo "  ok    the session record names its repo, so fleet sessions can tell two repos' branches apart" || { echo "  FAIL  no repo on the session record"; fails=$((fails+1)); }
cp "$ORG_STATE/roles.map.d17" "$ORG_STATE/roles.map"

# Defect 6: no resume signal reached a running session. `fleet resume` deletes a file; nothing tells
# a session already running, so it keeps standing down from its memory of the refusal — the one
# repeated correction of the rehearsal. The lift must be delivered like the stand-down was.
S5=local_5555eeee
run "a fresh session in repo2 starts clean"                                  0 "$(ev hook_event_name=SessionStart session_id=$S5 cwd=$REPO2 source=startup)"
(cd "$work/repo2" && "$PY" "$F" stop feat/y "defect 6 check") >/dev/null
run "it is refused by the stop"                                              2 "$(tool PreToolUse $S5 $REPO2 Bash t30 '{"command":"git status"}')"
out=$(printf '%s' "$(ev hook_event_name=UserPromptSubmit session_id=$S5 cwd=$REPO2 prompt=hi)" | "$PY" "$H")
case "$out" in *"no stop flag"*) echo "  FAIL  told the flag was lifted while it is still set"; fails=$((fails+1));; *) echo "  ok    while the stop stands, no resume line is injected";; esac
(cd "$work/repo2" && "$PY" "$F" resume feat/y) >/dev/null
out=$(printf '%s' "$(ev hook_event_name=UserPromptSubmit session_id=$S5 cwd=$REPO2 prompt=hi)" | "$PY" "$H")
case "$out" in *"no stop flag on feat/y"*) echo "  ok    after resume, the next turn is told the stand-down is lifted";; *) echo "  FAIL  resume never reached the running session: $out"; fails=$((fails+1));; esac
out=$(printf '%s' "$(ev hook_event_name=UserPromptSubmit session_id=$S5 cwd=$REPO2 prompt=hi)" | "$PY" "$H")
case "$out" in *"no stop flag on feat/y"*) echo "  FAIL  the resume line repeats every turn"; fails=$((fails+1));; *) echo "  ok    the resume line is said once, not every turn";; esac

# Findings 2 (retire path b) and 3: `fleet revoke feat/y --to S5` in repo2. S4 holds repo2's feat/y.
# The revoked-to session must not be told it is stopped at SessionStart (3); when it ends, the flag
# written in its favour goes with it and its lease is released, so the next session on the branch is
# not refused with a stale reason naming a supervisor verb (2b).
(cd "$work/repo2" && "$PY" "$F" revoke feat/y --to local_5555 "finding 2/3 check") >/dev/null
out=$(printf '%s' "$(ev hook_event_name=SessionStart session_id=$S5 cwd=$REPO2 source=resume)" | "$PY" "$H")
case "$out" in *"STOP flag"*) echo "  FAIL  the revoked-to session is told it is stopped at SessionStart"; fails=$((fails+1));; *) echo "  ok    SessionStart does not tell the revoked-to session it is stood down";; esac
[ -n "$(ls "$FLEET_STATE/stop"/*__feat__y.json 2>/dev/null)" ] && echo "  ok    the revoke's flag stands while the except session is alive and undelivered" || { echo "  FAIL  revoke flag missing before delivery"; fails=$((fails+1)); }
run "the revoked-to session ends"                                            0 "$(ev hook_event_name=SessionEnd session_id=$S5 cwd=$REPO2 reason=exit)"
[ -z "$(ls "$FLEET_STATE/stop"/*__feat__y.json 2>/dev/null)" ] && echo "  ok    the flag written in its favour retires when the except session ends" || { echo "  FAIL  revoke flag outlived the session it was written for"; fails=$((fails+1)); }
run "the next session writes to repo2's feat/y with no stale stop in the way" 0 "$(tool PreToolUse $S4 $REPO2 Edit t31 "{\"file_path\":\"$REPO2/b.ts\"}")"
(cd "$work/repo2" && "$PY" "$F" stop feat/y "plain stop, no except") >/dev/null
run "a plain stop still refuses"                                             2 "$(tool PreToolUse $S4 $REPO2 Bash t32 '{"command":"git status"}')"
[ -n "$(ls "$FLEET_STATE/stop"/*__feat__y.json 2>/dev/null)" ] && echo "  ok    a plain stop (no except) is not retired by delivery; it stands until fleet resume" || { echo "  FAIL  a plain stop was retired on first denial"; fails=$((fails+1)); }
(cd "$work/repo2" && "$PY" "$F" resume feat/y) >/dev/null

# Finding 4: the fallback inflight/lock key used hash(cmd), which Python salts per interpreter, and
# PreToolUse and PostToolUse are two interpreters — so with no tool_use_id (every Codex command) the
# post hook never found the record, wrote no ledger row and never released the suite lock. And a
# session that ended mid-command left its lock behind for good.
S7=local_7777aaaa
noid() { "$PY" -c "import json,sys; print(json.dumps({'hook_event_name':sys.argv[1],'session_id':sys.argv[2],'cwd':sys.argv[3],'tool_name':'Bash','tool_input':{'command':sys.argv[4]},'tool_response':{}}))" "$@"; }
run "a command with no tool_use_id starts (fallback key)"                    0 "$(noid PreToolUse $S7 $REPO2 'FLEET_ALLOW_SLOW=codex npx vitest run')"
s7_inflight() { ls "$FLEET_STATE/inflight"/${S7}-*.json 2>/dev/null; }   # only this session's; an earlier allowed-but-never-closed scenario leaves its own record behind
[ -n "$(s7_inflight)" ] && echo "  ok    inflight record written under the fallback key" || { echo "  FAIL  no inflight record"; fails=$((fails+1)); }
age "$(s7_inflight | head -1)" at 700
run "its PostToolUse finds the same record without a tool_use_id"            0 "$(noid PostToolUse $S7 $REPO2 'FLEET_ALLOW_SLOW=codex npx vitest run')"
[ -z "$(s7_inflight)" ] && echo "  ok    the fallback key is stable across the two hook processes (inflight consumed)" || { echo "  FAIL  PostToolUse computed a different fallback key; inflight leaked"; fails=$((fails+1)); }
"$PY" - "$FLEET_STATE/costs.jsonl" <<'PY' && echo "  ok    the no-id command still lands in the ledger (~700s)" || { echo "  FAIL  no ledger row for the no-id command"; fails=$((fails+1)); }
import json,sys
rows=[json.loads(l) for l in open(sys.argv[1],encoding="utf-8")]
sys.exit(0 if any(r["sig"]=="npx vitest" and 700 <= r["seconds"] < 760 for r in rows) else 1)
PY
run "a suite starts and its session dies mid-run"                           0 "$(noid PreToolUse $S7 $REPO2 'FLEET_ALLOW_SLOW=x npx vitest run')"
run "SessionEnd of the runner"                                              0 "$(ev hook_event_name=SessionEnd session_id=$S7 cwd=$REPO2 reason=exit)"
[ -z "$(grep -l "$S7" "$FLEET_STATE/locks"/*.json 2>/dev/null)" ] && [ -z "$(s7_inflight)" ] && echo "  ok    a session's suite lock and inflight records die with it" || { echo "  FAIL  lock or inflight outlived the session: $(ls "$FLEET_STATE/locks" "$FLEET_STATE/inflight")"; fails=$((fails+1)); }
run "another session may now run the suite"                                 0 "$(tool PreToolUse $S4 $REPO2 Bash t33 '{"command":"FLEET_ALLOW_SLOW=x npx vitest run"}')"
run "and closes it cleanly"                                                 0 "$(tool PostToolUse $S4 $REPO2 Bash t33 '{"command":"FLEET_ALLOW_SLOW=x npx vitest run"}')"

# Defect 4: the two files `fleet role` writes into a checkout were not ignored anywhere, so they sat
# untracked one `git add -A` from being committed into a shared repo. Ask git, not the exclude file:
# what matters is that the checkout ignores them, and `fleet role` ran against the WORKTREE, whose
# info/exclude lives in the common dir — so the main checkout must be covered by that same write.
"$PY" "$F" role "$WT" finisher:cam >/dev/null 2>&1
for target in "$WT" "$REPO"; do
  ignored=$(git -C "$target" check-ignore CLAUDE.local.md .claude/settings.local.json 2>/dev/null | wc -l | tr -d ' ')   # BSD wc pads with spaces
  [ "$ignored" = 2 ] && echo "  ok    git ignores both role artifacts in $(basename "$target")" || { echo "  FAIL  $target ignores only $ignored/2 role artifacts"; fails=$((fails+1)); }
done
[ -z "$(git -C "$WT" status --porcelain --untracked-files=all | grep -E 'CLAUDE\.local\.md|settings\.local\.json')" ] && echo "  ok    a roled worktree is clean: the artifacts do not show as untracked" || { echo "  FAIL  role artifacts still show in git status"; fails=$((fails+1)); }

# Defect 22: `fleet role` merges its denies into a settings.local.json a human maintains by hand
# (the monorepo's has 72 allow rules) and wrote it back compact, collapsing the file to one line.
# Content survived; the operator's next diff did not. Same principle as 15: only add the line.
mkdir -p "$work/repo2/.claude"   # not $WT: an earlier scenario leaves a quoted developer_instructions there, which fleet role rightly refuses
printf '{\n  "permissions": {\n    "allow": [\n      "Bash(gh pr view:*)"\n    ]\n  }\n}\n' > "$work/repo2/.claude/settings.local.json"
"$PY" "$F" role "$REPO2" finisher:other >/dev/null 2>&1
"$PY" - "$work/repo2/.claude/settings.local.json" <<'PY' && echo "  ok    fleet role keeps a hand-maintained settings.local.json readable" || { echo "  FAIL  settings.local.json was collapsed or lost content"; fails=$((fails+1)); }
import json, sys
raw = open(sys.argv[1], encoding="utf-8").read()
d = json.loads(raw)
bad = []
if raw.count(chr(10)) < 5:
    bad.append("collapsed to %d line(s); a hand-maintained file must stay indented" % (raw.count(chr(10)) + 1))
if "Bash(gh pr view:*)" not in d["permissions"].get("allow", []):
    bad.append("pre-existing allow rule was dropped")
if "Bash(gh pr merge:*)" not in d["permissions"].get("deny", []):
    bad.append("the role's deny was not merged in")
for b in bad:
    print("    " + b)
sys.exit(1 if bad else 0)
PY

# Defect 15: `fleet role` rewrote the line endings of the WHOLE roles.map, because Python text mode
# translates \n to \r\n on Windows. org's boot hook parses with `while read -r prefix tenant role`,
# so the \r rides along on the role, `org boot` gets `supervisor:sidebar\r`, finds nothing and exits
# 0 — org's lanes stop booting with no error. Assert we touch only the line we were asked to touch.
cp "$ORG_STATE/roles.map" "$ORG_STATE/roles.map.d15"
printf 'C:/x work steward:sidebar\nC:/y work verifier:sidebar\n' > "$ORG_STATE/roles.map"
"$PY" "$F" role "$WORK" author:cam >/dev/null 2>&1
"$PY" - "$ORG_STATE/roles.map" <<'PY' && echo "  ok    fleet role leaves the rest of roles.map byte-identical (no CRLF rewrite)" || { echo "  FAIL  fleet role rewrote line endings or existing lines"; fails=$((fails+1)); }
import sys
d = open(sys.argv[1], "rb").read()
crlf = d.count(b"\r\n")
kept = b"C:/x work steward:sidebar\nC:/y work verifier:sidebar\n" in d
if crlf:
    print("    %d CRLF line endings written into an LF file" % crlf)
if not kept:
    print("    pre-existing lines were altered: %r" % d[:120])
sys.exit(0 if (not crlf and kept) else 1)
PY
cp "$ORG_STATE/roles.map.d15" "$ORG_STATE/roles.map"

# Defect 2: `fleet role` wrote a literal `work` into the tenant column and replaced whatever line
# already claimed the path. Org's hooks read that column as the truth, so it retagged the checkout's
# tenant; and on the Mac it silently ate `workbench mh lead:agentic-development`. Both halves here.
printf '%s mh lead:agentic-development\n' "$REPO" > "$ORG_STATE/roles.map.t2"
cp "$ORG_STATE/roles.map" "$ORG_STATE/roles.map.keep"
cp "$ORG_STATE/roles.map.t2" "$ORG_STATE/roles.map"
out=$("$PY" "$F" role "$REPO" supervisor:cam 2>&1)
if [ $? != 0 ] && grep -q "lead:agentic-development" <<EOF
$out
EOF
then echo "  ok    fleet role refuses to replace a non-fleet binding and names it"; else echo "  FAIL  fleet role replaced another system's binding: $out"; fails=$((fails+1)); fi
grep -q "mh lead:agentic-development" "$ORG_STATE/roles.map" && echo "  ok    the refused run left roles.map untouched" || { echo "  FAIL  refused run still rewrote roles.map: $(cat "$ORG_STATE/roles.map")"; fails=$((fails+1)); }
"$PY" "$F" role "$REPO" supervisor:cam --force >/dev/null 2>&1
grep -q " mh supervisor:cam$" "$ORG_STATE/roles.map" && echo "  ok    --force takes it over and keeps the tenant column (mh, not work)" || { echo "  FAIL  tenant column not preserved: $(cat "$ORG_STATE/roles.map")"; fails=$((fails+1)); }
# Finding 5 (Q3 ruling): a new line's tenant is inherited by prefix, else $ORG_TENANT, else refused
# naming --tenant. Never a literal default: org defaulted to `mh`, this wrote `work`, and the
# worktree resolved to a tenant with no chain — org's hooks then exited 0 with nothing to say.
mkdir -p "$work/repo/.claude/worktrees/inherit"
"$PY" "$F" role "$REPO/.claude/worktrees/inherit" author:cam >/dev/null 2>&1
grep -q "/inherit mh author:cam$" "$ORG_STATE/roles.map" && echo "  ok    a nested worktree inherits its tenant from the checkout's line (mh), never a literal" || { echo "  FAIL  nested worktree did not inherit the tenant: $(cat "$ORG_STATE/roles.map")"; fails=$((fails+1)); }
out=$(env -u ORG_TENANT "$PY" "$F" role "$WORK" infra:cc-skills 2>&1); rc=$?
case "$rc:$out" in 0:*) echo "  FAIL  a line with nothing to inherit was written with a default tenant: $(grep infra "$ORG_STATE/roles.map")"; fails=$((fails+1));; *"--tenant"*) echo "  ok    nothing to inherit and no ORG_TENANT → refused, naming --tenant";; *) echo "  FAIL  refusal did not name the flag: $out"; fails=$((fails+1));; esac
grep -q "infra:cc-skills" "$ORG_STATE/roles.map" && { echo "  FAIL  the refused run still wrote a line"; fails=$((fails+1)); } || echo "  ok    the refused run wrote nothing"
# An ancestor is a path component, not a string prefix: `<work>/repo` is not above `<work>/repo-sib`.
mkdir -p "$work/repo-sib"
out=$(env -u ORG_TENANT "$PY" "$F" role "$work/repo-sib" author:cam 2>&1); rc=$?
[ "$rc" != 0 ] && ! grep -q "repo-sib" "$ORG_STATE/roles.map" && echo "  ok    a sibling path does not inherit a tenant from its string-prefix neighbour" || { echo "  FAIL  sibling inherited a tenant by string prefix: rc=$rc $(grep repo-sib "$ORG_STATE/roles.map")"; fails=$((fails+1)); }
out=$("$PY" "$F" role "$WORK" infra:cc-skills --tenant --force 2>&1); rc=$?
[ "$rc" != 0 ] && ! grep -q "infra:cc-skills" "$ORG_STATE/roles.map" && echo "  ok    --tenant followed by an option is refused, not written as the tenant" || { echo "  FAIL  --tenant swallowed an option: rc=$rc $(grep infra "$ORG_STATE/roles.map")"; fails=$((fails+1)); }
out=$("$PY" "$F" role "$WORK" infra:cc-skills --tenant 2>&1); rc=$?
[ "$rc" != 0 ] && ! grep -q "infra:cc-skills" "$ORG_STATE/roles.map" && echo "  ok    a trailing --tenant is refused" || { echo "  FAIL  trailing --tenant accepted: rc=$rc"; fails=$((fails+1)); }
"$PY" "$F" role "$WORK" infra:cc-skills --tenant acme >/dev/null 2>&1
grep -q " acme infra:cc-skills$" "$ORG_STATE/roles.map" && echo "  ok    --tenant writes the named tenant" || { echo "  FAIL  --tenant ignored: $(cat "$ORG_STATE/roles.map")"; fails=$((fails+1)); }
"$PY" -c 'import sys; p=sys.argv[1]; s=open(p).read().splitlines(); open(p,"w",newline="").write("\n".join(l for l in s if "infra:cc-skills" not in l)+"\n")' "$ORG_STATE/roles.map"
ORG_TENANT=acme "$PY" "$F" role "$WORK" infra:cc-skills >/dev/null 2>&1
grep -q " acme infra:cc-skills$" "$ORG_STATE/roles.map" && echo "  ok    a new line takes its tenant from \$ORG_TENANT" || { echo "  FAIL  \$ORG_TENANT ignored for a new line: $(cat "$ORG_STATE/roles.map")"; fails=$((fails+1)); }
grep -q '\\' "$ORG_STATE/roles.map" && { echo "  FAIL  roles.map line written with backslashes; org's own hooks read this file: $(cat "$ORG_STATE/roles.map")"; fails=$((fails+1)); } || echo "  ok    roles.map paths are written forward-slashed, matching org's convention"
cp "$ORG_STATE/roles.map.keep" "$ORG_STATE/roles.map"

# fleet.py hardening (findings on #42's merge head): a malformed settings.local.json must be refused
# before anything is written; a git failure is a refusal, never T0; a path with a space is one path.
mkdir -p "$work/repo2/.claude"
printf '{ "permissions": { "allow": [ "Bash(gh pr view:*)" ], \n' > "$work/repo2/.claude/settings.local.json"   # truncated: invalid JSON
cp "$work/repo2/.claude/settings.local.json" "$work/settings.bad"
cp "$ORG_STATE/roles.map" "$ORG_STATE/roles.map.hard"
out=$("$PY" "$F" role "$REPO2" author:other 2>&1); rc=$?
[ "$rc" != 0 ] && cmp -s "$work/settings.bad" "$work/repo2/.claude/settings.local.json" && cmp -s "$ORG_STATE/roles.map.hard" "$ORG_STATE/roles.map" && echo "  ok    fleet role refuses a malformed settings.local.json and writes nothing" || { echo "  FAIL  malformed settings.local.json: rc=$rc; file or roles.map changed: $out"; fails=$((fails+1)); }
printf '{\n  "permissions": {\n    "allow": [\n      "Bash(gh pr view:*)"\n    ]\n  }\n}\n' > "$work/repo2/.claude/settings.local.json"
out=$( (cd "$work/wt" && "$PY" "$F" tier --base definitely-not-a-ref --json) 2>&1 ); rc=$?
case "$rc:$out" in 0:*) echo "  FAIL  a failed git diff was reported as a tier: $out"; fails=$((fails+1));; *'"tier"'*) echo "  FAIL  tier printed despite git failing"; fails=$((fails+1));; *git*) echo "  ok    fleet tier refuses when git cannot produce the diff, naming git's error";; *) echo "  FAIL  refusal does not name git: $out"; fails=$((fails+1));; esac
printf 'x\n' > "$work/wt/docs/slow case.md" && git -C "$work/wt" rm -q mystery.bin && git -C "$work/wt" add -A && git -C "$work/wt" -c user.email=t@t -c user.name=t commit -qm "space in path"   # mystery.bin (unclassified, from the tier scenario) would refuse first
out=$( (cd "$work/wt" && "$PY" "$F" tier --base feat/x --json) 2>&1 )
case "$out" in *'"docs/slow case.md"'*) echo "  ok    a changed path with a space is classified as one path";; *) echo "  FAIL  path with a space was split or refused: $(printf '%s' "$out" | head -2)"; fails=$((fails+1));; esac

# Defect 10 (Mac side): without psutil, SessionStart may spend its one permitted spawn on a ps walk
# to the harness pid. Stubbed `ps` on PATH so the chain is under our control; skipped where psutil
# is present because that path never reaches the walk.
if "$PY" -c 'import psutil' 2>/dev/null; then
  echo "  ok    ps walk scenarios skipped: psutil present, the walk is never reached on this box"
else
  mkdir -p "$work/fakebin"
  cat > "$work/fakebin/ps" <<'PS'
#!/usr/bin/env bash
# fake ps for `ps -o ppid=,comm= -p <pid>`: prints "<ppid> <comm>" for the queried pid. The first hop
# is a plain shell whose parent is the harness; querying the harness pid names the harness command.
# So the walk must actually follow a ppid, and the pid it records must be the harness's, not the shell's.
pid="${@: -1}"
case "$FAKE_PS_MODE:$pid" in
  claude:424242) printf '1 /Applications/Claude.app/Contents/MacOS/claude\n' ;;
  claude:*)      printf '424242 bash\n' ;;
  claude2:424244) printf '1 /Applications/Claude.app/Contents/MacOS/claude\n' ;;   # a second harness process, after a resume
  claude2:*)     printf '424244 bash\n' ;;
  codex:424243)  printf '1 /opt/homebrew/bin/codex\n' ;;
  codex:*)       printf '424243 bash\n' ;;
  *)             printf '1 launchd\n' ;;
esac
PS
  chmod +x "$work/fakebin/ps"
  S8=local_8888aaaa
  out=$(printf '%s' "$(ev hook_event_name=SessionStart session_id=$S8 cwd=$WT source=startup)" | PATH="$work/fakebin:$PATH" FAKE_PS_MODE=claude "$PY" "$H")
  "$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if r.get('pid_kind')=='harness' and r.get('pid')==424242 else 1)" "$FLEET_STATE/sessions/$S8.json" && echo "  ok    SessionStart without psutil walks ps one hop to the harness and records ITS pid (424242), not the shell's" || { echo "  FAIL  ps walk did not record the harness pid: $(cat "$FLEET_STATE/sessions/$S8.json")"; fails=$((fails+1)); }
  run "a later event does not spawn (record unchanged)"                       0 "$(tool PreToolUse $S8 $WT Bash t50 '{"command":"git status"}')"
  # A resume under a NEW harness process (the old one crashed) must re-resolve the pid, or the live
  # session reads as dead and its lease is taken over.
  out=$(printf '%s' "$(ev hook_event_name=SessionStart session_id=$S8 cwd=$WT source=resume)" | PATH="$work/fakebin:$PATH" FAKE_PS_MODE=claude2 "$PY" "$H")
  "$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if r.get('pid_kind')=='harness' and r.get('pid')==424244 else 1)" "$FLEET_STATE/sessions/$S8.json" && echo "  ok    a resume under a new harness process re-resolves the pid (424244), not the crashed one" || { echo "  FAIL  resume kept the old harness pid: $(cat "$FLEET_STATE/sessions/$S8.json")"; fails=$((fails+1)); }
  S10=local_aaaa0000
  out=$(printf '%s' "$(ev hook_event_name=SessionStart session_id=$S10 cwd=$WT source=startup)" | PATH="$work/fakebin:$PATH" FAKE_PS_MODE=codex "$PY" "$H")
  "$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if r.get('pid_kind')=='harness' and r.get('pid')==424243 else 1)" "$FLEET_STATE/sessions/$S10.json" && echo "  ok    a native Codex grandparent is a harness too, and its pid is the one recorded (424243)" || { echo "  FAIL  codex ancestor not recognised or wrong pid: $(cat "$FLEET_STATE/sessions/$S10.json")"; fails=$((fails+1)); }
  S9=local_9999aaaa
  out=$(printf '%s' "$(ev hook_event_name=SessionStart session_id=$S9 cwd=$WT source=startup)" | PATH="$work/fakebin:$PATH" FAKE_PS_MODE=none "$PY" "$H")
  "$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if r.get('pid_kind')=='parent-unverified' else 1)" "$FLEET_STATE/sessions/$S9.json" && echo "  ok    no harness ancestor → parent-unverified, stated not silent" || { echo "  FAIL  wrong basis with no harness ancestor: $(cat "$FLEET_STATE/sessions/$S9.json")"; fails=$((fails+1)); }
fi

# ---------- Phase 1 (TDD §4.3, §4.4, §4.6, §6, §7.2, §7.4, §8): resource leases, requires, receipts ----------
git -C "$work/repo" worktree add -q "$work/wt2" -b feat/z
WT2="$(native "$work/wt2")"
"$PY" "$F" role "$WT2" liverun:cam --tenant work >/dev/null 2>&1 || { echo "  FAIL  could not role the liverun worktree: $("$PY" "$F" role "$WT2" liverun:cam --tenant work 2>&1 | head -2)"; fails=$((fails+1)); }
S11=local_bbbb1111 S12=local_cccc2222
out=$(printf '%s' "$(ev hook_event_name=SessionStart session_id=$S11 cwd=$WT2 source=startup)" | "$PY" "$H")
case "$out" in *"requires slot:hyper"*"free"*) echo "  ok    SessionStart caches the lane and names the free resource it requires";; *) echo "  FAIL  SessionStart lane line: $out"; fails=$((fails+1));; esac
"$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); l=r.get('lane') or {}; sys.exit(0 if l.get('requires')==['slot:hyper'] and l.get('produces')=='live' else 1)" "$FLEET_STATE/sessions/$S11.json" && echo "  ok    the session record carries lane {requires, produces} from the manifest" || { echo "  FAIL  lane not cached: $(cat "$FLEET_STATE/sessions/$S11.json")"; fails=$((fails+1)); }
err=$(printf '%s' "$(tool PreToolUse $S11 $WT2 Edit t60 "{\"file_path\":\"$WT2/a.ts\"}")" | "$PY" "$H" 2>&1 >/dev/null); rc=$?
case "$rc:$err" in 2:*"fleet take slot:hyper"*) echo "  ok    an effectful call without the required resource is refused, naming fleet take";; *) echo "  FAIL  requires denial: rc=$rc $err"; fails=$((fails+1));; esac
run "chained 'fleet slots && touch x' is guarded (not a standalone verb)"   2 "$(tool PreToolUse $S11 $WT2 Bash t61 "{\"command\":\"$PY $F slots && touch x\"}")"
run "redirected 'fleet slots > x' is guarded"                                2 "$(tool PreToolUse $S11 $WT2 Bash t62 "{\"command\":\"$PY $F slots > x\"}")"
run "substituted 'fleet slots \"\$(touch x)\"' is guarded"                    2 "$(tool PreToolUse $S11 $WT2 Bash t63 "{\"command\":\"$PY $F slots \\\"\$(touch x)\\\"\"}")"
run "'fleet receipt' without the resource is guarded like Edit"              2 "$(tool PreToolUse $S11 $WT2 Bash t64 "{\"command\":\"$PY $F receipt abc123 live pass \\\"x\\\"\"}")"
run "a newline-smuggled second command is guarded, not exempt"               2 "$(tool PreToolUse $S11 $WT2 Bash t70 "{\"command\":\"$PY $F slots\\ntouch pwned\"}")"
run "a standalone 'fleet take' passes the requires guard"                    0 "$(tool PreToolUse $S11 $WT2 Bash t65 "{\"command\":\"$PY $F take slot:hyper \\\"packet abc\\\"\"}")"
run "a standalone 'fleet leases' passes the requires guard"                  0 "$(tool PreToolUse $S11 $WT2 Bash t66 "{\"command\":\"$PY $F leases\"}")"
(cd "$work/wt2" && "$PY" "$F" take slot:hyper "packet abc" >/dev/null) && echo "  ok    fleet take leases the resource for the session at this cwd" || { echo "  FAIL  fleet take refused: $(cd "$work/wt2" && "$PY" "$F" take slot:hyper x 2>&1)"; fails=$((fails+1)); }
grep -q "\"session\": \"$S11\"" "$FLEET_STATE/leases/slot__hyper.json" 2>/dev/null && echo "  ok    the resource lease is keyed slot:<name> and names the session" || { echo "  FAIL  slot lease missing or wrong: $(ls "$FLEET_STATE/leases")"; fails=$((fails+1)); }
[ -z "$(ls -a "$FLEET_STATE/leases" | grep '^\.tmp\.')" ] && echo "  ok    the temp file from the take is gone (linked into place, then unlinked)" || { echo "  FAIL  stray temp after a take: $(ls -a "$FLEET_STATE/leases")"; fails=$((fails+1)); }
run "with the resource held, the same Edit is allowed"                      0 "$(tool PreToolUse $S11 $WT2 Edit t67 "{\"file_path\":\"$WT2/a.ts\"}")"
(cd "$work/wt2" && "$PY" "$F" take slot:hyper "again" >/dev/null) && echo "  ok    taking a resource you already hold is a no-op, not a refusal" || { echo "  FAIL  re-take refused"; fails=$((fails+1)); }
run "a second session starts in the main checkout"                          0 "$(ev hook_event_name=SessionStart session_id=$S12 cwd=$REPO source=startup)"
out=$( (cd "$work/repo" && "$PY" "$F" take slot:hyper "mine") 2>&1 ); rc=$?
case "$rc:$out" in 0:*) echo "  FAIL  a held resource was taken by a second session"; fails=$((fails+1));; *"$(echo $S11 | cut -c1-8)"*"fleet revoke slot:hyper --to"*) echo "  ok    a held resource refuses a second taker, naming the holder and the revoke redirect";; *) echo "  FAIL  contested take text: $out"; fails=$((fails+1));; esac
out=$( (cd "$work/repo" && "$PY" "$F" drop slot:hyper) 2>&1 ); rc=$?
[ "$rc" != 0 ] && case "$out" in *"not by you"*) echo "  ok    only the holder may drop a resource";; *) echo "  FAIL  drop refusal text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  a non-holder dropped the resource"; fails=$((fails+1)); }
# Exclusive publication (§4.3, §8): eight takers at once, one key, exactly one holder, and every
# loser reads a complete winner record — never a partial file. A file, not stdin: macOS's spawn
# start method re-imports the main module, which does not exist for a heredoc script.
cat > "$work/race.py" <<'PY'
import json, os, sys, multiprocessing as mp
def taker(i):
    import xlib as hook
    ok = hook.acquire_lease("slot:race", hook.lease_record("slot:race", f"sess{i:02d}", "r", "/tmp", "race"))
    seen = hook.lease("slot:race")
    return ok, (seen or {}).get("session"), bool(seen and not seen.get("malformed"))
if __name__ == "__main__":
    os.environ["FLEET_STATE"] = sys.argv[1]
    sys.path.insert(0, sys.argv[2])
    import xlib as hook
    with mp.Pool(8) as pool:
        results = pool.map(taker, range(8))
    winners = [r for r in results if r[0]]
    final = hook.lease("slot:race")
    good = len(winners) == 1 and all(r[2] for r in results) and all(r[1] == final["session"] for r in results)
    if not good:
        print("    results:", results, "final:", final)
    sys.exit(0 if good else 1)
PY
FLEET_STATE="$FLEET_STATE" "$PY" "$work/race.py" "$FLEET_STATE" "$FLEET_HOOK_DIR" && echo "  ok    eight concurrent takers → exactly one holder; every loser read a complete winner" || { echo "  FAIL  concurrent acquisition is not exclusive or exposed a partial record"; fails=$((fails+1)); }
rm -f "$FLEET_STATE/leases/slot__race.json"

cat > "$work/race2.py" <<'PY'
import json, os, sys, multiprocessing as mp


def contend(i):
    import xlib as hook
    reason = hook.check_lease("repo:r:feat/race", "feat/race", f"live{i:02d}", "role", "/tmp")
    return reason is None


if __name__ == "__main__":
    os.environ["FLEET_STATE"] = sys.argv[1]
    sys.path.insert(0, sys.argv[2])
    import xlib as hook
    # A dead holder, and eight live sessions that all observe it at once. Every one of them is
    # entitled to take a dead BRANCH holder over, so every one races through steal + acquire.
    os.makedirs(hook.path("sessions"), exist_ok=True)
    hook.write_json(hook.path("sessions", "deadone.json"),
                    {"session": "deadone", "pid": 999999, "pid_kind": "harness", "last_event_at": 0})
    for i in range(8):
        hook.write_json(hook.path("sessions", f"live{i:02d}.json"),
                        {"session": f"live{i:02d}", "pid_kind": "parent-unverified", "last_event_at": hook.now()})
    hook.acquire_lease("repo:r:feat/race", hook.lease_record("repo:r:feat/race", "deadone", "role", "/tmp", "dead"))
    with mp.Pool(8) as pool:
        authorized = pool.map(contend, range(8))
    final = hook.lease("repo:r:feat/race")
    winners = [f"live{i:02d}" for i, ok in enumerate(authorized) if ok]
    good = (len(winners) == 1 and final and not final.get("malformed")
            and final.get("session") == winners[0])
    if not good:
        print("    authorized:", winners, "final:", final)
    sys.exit(0 if good else 1)
PY
"$PY" "$work/race2.py" "$FLEET_STATE" "$FLEET_HOOK_DIR" && echo "  ok    eight sessions racing to take over one dead branch holder → exactly one is authorized" || { echo "  FAIL  concurrent dead-holder takeover authorized more than one writer"; fails=$((fails+1)); }
rm -f "$FLEET_STATE/leases/repo__r__feat__race.json" "$FLEET_STATE/sessions/deadone.json" "$FLEET_STATE"/sessions/live*.json

# The two race fixes, proven by forcing the interleaving rather than hoping for it: one session is
# paused just after it reads the key, a rival settles the key underneath it, and the paused session
# then proceeds on its stale observation. Exactly one of the two may end up authorized.
cat > "$work/interleave.py" <<'PY'
import json, os, subprocess, sys, time
state, hookdir, mode = sys.argv[1], sys.argv[2], sys.argv[3]
os.environ["FLEET_STATE"] = state
sys.path.insert(0, hookdir)
import xlib as hook
key = "repo:r:feat/inter"
for s in ("slowone", "fastone"):
    hook.write_json(hook.path("sessions", f"{s}.json"),
                    {"session": s, "pid_kind": "parent-unverified", "last_event_at": hook.now()})
if mode == "dead":                       # a dead holder both sessions are entitled to take over
    hook.write_json(hook.path("sessions", "deadinter.json"),
                    {"session": "deadinter", "pid": 999999, "pid_kind": "harness", "last_event_at": 0})
    hook.acquire_lease(key, hook.lease_record(key, "deadinter", "r", "/tmp", "dead"))
probe = ("import os,sys; os.environ['FLEET_STATE']=sys.argv[1]; sys.path.insert(0, sys.argv[2]); import xlib as hook; "
         "sys.exit(0 if hook.check_lease(sys.argv[3], 'feat/inter', sys.argv[4], 'r', '/tmp') is None else 1)")
slow = subprocess.Popen([sys.executable, "-c", probe, state, hookdir, key, "slowone"],
                        env={**os.environ, "FLEET_TEST_PAUSE_AFTER_OBSERVE": "1.5"},
                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
time.sleep(0.4)                          # the rival settles the key while the slow one is paused
fast = subprocess.run([sys.executable, "-c", probe, state, hookdir, key, "fastone"],
                      env={**os.environ}, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
authorized = [n for n, rc in (("slowone", slow.wait()), ("fastone", fast.returncode)) if rc == 0]
final = hook.lease(key)
good = len(authorized) == 1 and final and final.get("session") == authorized[0]
if not good:
    print("    authorized:", authorized, "final:", final)
hook._unlink(hook.key_file("leases", key))          # only this scenario's key: later scenarios hold theirs
for n in ("slowone", "fastone", "deadinter"):
    hook._unlink(hook.path("sessions", f"{n}.json"))
sys.exit(0 if good else 1)
PY
"$PY" "$work/interleave.py" "$FLEET_STATE" "$FLEET_HOOK_DIR" free && echo "  ok    a race loser on a free key acquires or refuses — it is never authorized holding nothing" || { echo "  FAIL  a race loser was authorized without holding the lease"; fails=$((fails+1)); }
"$PY" "$work/interleave.py" "$FLEET_STATE" "$FLEET_HOOK_DIR" dead && echo "  ok    a stale dead-holder takeover cannot delete the winner's fresh lease" || { echo "  FAIL  a stale takeover removed the winner's lease and authorized a second writer"; fails=$((fails+1)); }

# Round two. Every P1 below was one defect wearing three hats: a decision taken on a RE-READ rather
# than on the record that was actually observed. Each scenario forces the interleaving that exposes
# it — a probe that races N processes and hopes stays green against all three.
cat > "$work/round2.py" <<'PY'
import json, os, subprocess, sys, time
state, hookdir, fleetpy, cwd, mode = sys.argv[1:6]
os.environ["FLEET_STATE"] = state
sys.path.insert(0, hookdir)
import xlib as hook

def session(sid, alive=True, **kw):
    rec = {"session": sid, "cwd": cwd, "role": "r", **kw}
    rec.update({"pid_kind": "parent-unverified", "last_event_at": hook.now()} if alive
               else {"pid": 999999, "pid_kind": "harness", "last_event_at": 0})
    hook.write_json(hook.path("sessions", f"{sid}.json"), rec)

def cleanup(key, *sids):
    hook._unlink(hook.key_file("leases", key))
    for n in sids:
        hook._unlink(hook.path("sessions", f"{n}.json"))
    d = hook.path("claims")
    for n in os.listdir(d) if os.path.isdir(d) else []:
        hook._unlink(os.path.join(d, n))

quiet = {"stdout": subprocess.DEVNULL, "stderr": subprocess.DEVNULL}

if mode == "takeover":
    # Two operators take over the SAME orphan. The slow one is paused just after it classifies the
    # holder as dead; the fast one completes and is now the live holder. The slow one must displace
    # the orphan it saw or nothing at all. Re-reading the key here hands it the fast one's LIVE
    # session as the record to displace, and both report a successful takeover.
    key = "slot:racy"
    session("deadslot", alive=False)
    session("slowtk"); session("fasttk")
    hook.acquire_lease(key, hook.lease_record(key, "deadslot", "r", cwd, "dead"))
    take = lambda sid, env: subprocess.Popen(
        [sys.executable, fleetpy, "take", key, "checked", "--takeover", "--session", sid],
        cwd=cwd, env={**os.environ, **env}, **quiet)
    slow = take("slowtk", {"FLEET_TEST_PAUSE_AFTER_KEY_READ": "1.5"})
    time.sleep(0.4)
    fast = take("fasttk", {})
    won = [n for n, rc in (("slowtk", slow.wait()), ("fasttk", fast.wait())) if rc == 0]
    final = hook.lease(key)
    good = len(won) == 1 and final and final.get("session") == won[0]
    if not good:
        print("    took over:", won, "final:", final)
    cleanup(key, "deadslot", "slowtk", "fasttk")

elif mode == "migrate":
    # Installing phase 1 over a live ~/.fleet is FAIL-OPEN without re-keying: the new code looks for
    # `repo__<id>__<branch>.json`, finds nothing, and authorizes a second writer on a branch whose
    # incumbent still holds `<id>__<branch>.json` — with that branch's stop flag equally invisible.
    rid, branch = "legacyrepo-1234abcd", "feat/legacy"
    key = f"repo:{rid}:{branch}"
    session("incumbent")
    for sub, rec in (("leases", {"branch": branch, "repo": rid, "session": "incumbent", "role": "r",
                                 "cwd": cwd, "since": hook.now(), "last": hook.now(), "note": "old format"}),
                     ("stop", {"branch": branch, "repo": rid, "reason": "legacy stop", "by": "operator",
                               "at": hook.now(), "except": None, "holder": "incumbent"})):
        os.makedirs(hook.path(sub), exist_ok=True)
        hook.write_json(hook.path(sub, f"{rid}__{hook.safe(branch)}.json"), rec)   # the pre-phase-1 name
    hook._unlink(hook.path("migrated-keys.v1"))     # simulate the install landing on live state
    hook.migrate_legacy_keys()
    held = hook.lease(key)
    flag = hook.stop_flag(key)
    # A newcomer must now be refused: the incumbent is live and holds the re-keyed lease.
    refusal = hook.check_lease(key, branch, "newcomer", "r", cwd)
    legacy_gone = not os.path.exists(hook.path("leases", f"{rid}__{hook.safe(branch)}.json"))
    marked = os.path.exists(hook.path("migrated-keys.v1"))   # a completed pass short-circuits later events
    hook.migrate_legacy_keys()                       # idempotent: a second pass changes nothing
    still = hook.lease(key)
    good = (held and held.get("session") == "incumbent" and held.get("kind") == "branch"
            and held.get("key") == key and flag and flag.get("reason") == "legacy stop"
            and refusal is not None and legacy_gone and still == held and marked)
    if not good:
        print("    held:", held, "flag:", flag, "refusal:", refusal, "legacy_gone:", legacy_gone)
    hook._unlink(hook.key_file("stop", key))
    cleanup(key, "incumbent")

elif mode == "keymismatch":
    # safe() maps both `/` and `:` to `__`, so `feat/x` and `feat__x` — both legal git names — land
    # on one filename. A record must name the key it was read under, or one branch's session reads
    # the other's lease as authoritative for its own branch.
    a, b = "repo:r:feat/collide", "repo:r:feat__collide"
    session("owner-a")
    hook.acquire_lease(a, hook.lease_record(a, "owner-a", "r", cwd, "held"))
    same_file = hook.key_file("leases", a) == hook.key_file("leases", b)
    other = hook.lease(b)
    good = same_file and isinstance(other, dict) and other.get("malformed")
    if not good:
        print("    same file:", same_file, "| read under the other key:", other)
    cleanup(a, "owner-a")

elif mode == "failclosed":
    # The lease path must deny on ANY error, not fall through to main's catch-all exit 0. Two ways
    # in: a refusal that raises while being built (a hand-edited record), and a store that cannot be
    # written. Both used to authorize the write against a live foreign holder.
    key = "repo:r:feat/failclosed"
    session("liveholder")
    hook.acquire_lease(key, hook.lease_record(key, "liveholder", "r", cwd, "held"))
    bad = hook.key_file("leases", key)
    rec = hook.read_json(bad)
    rec["since"] = "yesterday"                       # fmt_age(now() - "yesterday") raises TypeError
    hook.write_json(bad, rec)
    denial = hook.check_lease(key, "feat/failclosed", "newcomer", "r", cwd)
    good = denial is not None
    if not good:
        print("    a refusal that raised authorized the write instead:", denial)
    cleanup(key, "liveholder")

elif mode == "lockfiles":
    # A cost lock stores the inflight id in a field named `key`. Trusting the field alone took a
    # keylock on a tool-use id and left a lock file per command, for ever.
    hook.write_json(hook.path("locks", "suite.json"),
                    {"session": "gone", "at": hook.now(), "key": "abc123-not-a-lease-key"})
    hook._remove_owned("locks", "gone")
    d = hook.path("keylocks")
    strays = [n for n in os.listdir(d) if "abc123" in n] if os.path.isdir(d) else []
    good = not os.path.exists(hook.path("locks", "suite.json")) and not strays
    if not good:
        print("    lock released:", not os.path.exists(hook.path("locks", "suite.json")), "strays:", strays)

elif mode == "drop-revoked":
    # `fleet drop` must not delete a lease that was revoked away from the dropper while it decided.
    # The dropper is paused just after it reads the key; the operator hands the key to someone else;
    # the dropper resumes holding a stale "it is mine". A pathname delete here silently undoes the
    # handoff and leaves the resource reading as free while its new holder drives it.
    key = "slot:dropped"
    session("dropper"); session("handee")
    hook.acquire_lease(key, hook.lease_record(key, "dropper", "r", cwd, "held"))
    drop = subprocess.Popen([sys.executable, fleetpy, "drop", key, "--session", "dropper"], cwd=cwd,
                            env={**os.environ, "FLEET_TEST_PAUSE_AFTER_KEY_READ": "1.5"}, **quiet)
    time.sleep(0.4)
    hook.take_lease(key, None, "handee", "r", cwd, "revoked mid-drop")
    rc = drop.wait()
    final = hook.lease(key)
    good = rc != 0 and final and final.get("session") == "handee"
    if not good:
        print("    drop rc:", rc, "final:", final)
    cleanup(key, "dropper", "handee")

elif mode == "requires":
    # The requires guard must decide on ONE observation, and the lease it finds must be OURS.
    # Reading the key free, then re-reading it non-null after a rival took it, let both sessions
    # past the guard and onto one resource.
    key = "slot:req-race"                # never slot:hyper: this scenario cleans up after itself
    session("needy", lane={"kind": "liverun", "requires": [key], "produces": "live"})
    session("rival")
    grab = ("import os,sys,time; os.environ['FLEET_STATE']=sys.argv[1]; sys.path.insert(0, sys.argv[2]); import xlib as hook; "
            "time.sleep(0.4); hook.acquire_lease(sys.argv[3], hook.lease_record(sys.argv[3],'rival','r',sys.argv[4],'mine'))")
    race = subprocess.Popen([sys.executable, "-c", grab, state, hookdir, key, cwd], **quiet)
    os.environ["FLEET_TEST_PAUSE_AFTER_KEY_READ"] = "1.5"
    denial = hook.check_requires(hook.read_json(hook.path("sessions", "needy.json")), "needy",
                                 "Edit", {"file_path": os.path.join(cwd, "a.ts")})
    os.environ.pop("FLEET_TEST_PAUSE_AFTER_KEY_READ")
    race.wait()
    holder = hook.lease(key)
    good = denial is not None and holder and holder.get("session") == "rival"
    if not good:
        print("    denial:", denial, "holder:", holder)
    cleanup(key, "needy", "rival")

sys.exit(0 if good else 1)
PY
r2() { "$PY" "$work/round2.py" "$FLEET_STATE" "$FLEET_HOOK_DIR" "$F" "$REPO" "$1"; }
r2 takeover   && echo "  ok    two takeovers of one orphan: exactly one succeeds, on the record it observed" || { echo "  FAIL  a stale takeover displaced the winner's live lease and also reported success"; fails=$((fails+1)); }
r2 migrate    && echo "  ok    legacy per-branch state is re-keyed before any key is read" || { echo "  FAIL  a legacy lease was invisible under the new key and a second session was authorized"; fails=$((fails+1)); }
r2 keymismatch && echo "  ok    a lease record must name the key it was read under (safe() collides)" || { echo "  FAIL  one branch read another branch's lease as its own"; fails=$((fails+1)); }
r2 failclosed && echo "  ok    a refusal that raises denies the write instead of authorizing it" || { echo "  FAIL  an error on the lease path became an authorization"; fails=$((fails+1)); }
r2 lockfiles  && echo "  ok    releasing a non-lease record takes no key lock and leaves no lock file" || { echo "  FAIL  a keylock was taken on something that is not a lease key"; fails=$((fails+1)); }
r2 drop-revoked && echo "  ok    a drop refuses once the key was revoked away mid-decision" || { echo "  FAIL  drop deleted a lease that had been revoked to another session"; fails=$((fails+1)); }
r2 requires   && echo "  ok    the requires guard denies when the resource is taken during its own decision" || { echo "  FAIL  a session was authorized onto a resource held by another session"; fails=$((fails+1)); }
# A newline is a command separator, so an exempt line may not contain one (round one).
# A dead resource holder is orphaned, never taken over silently (§4.6, §7.5).
"$PY" -c "import json,sys; p=sys.argv[1]; r=json.load(open(p)); r['pid']=999999; r['pid_kind']='harness'; json.dump(r,open(p,'w'))" "$FLEET_STATE/sessions/$S11.json"
out=$( (cd "$work/repo" && "$PY" "$F" take slot:hyper "mine") 2>&1 ); rc=$?
[ "$rc" != 0 ] && case "$out" in *"--takeover"*) echo "  ok    a dead holder's resource is refused as orphaned, naming --takeover";; *) echo "  FAIL  orphan text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  a dead holder's resource was taken over silently"; fails=$((fails+1)); }
"$PY" "$F" leases | grep -q "slot:hyper.*orphaned" && echo "  ok    fleet leases marks the orphaned resource, resource rows first" || { echo "  FAIL  leases listing: $("$PY" "$F" leases)"; fails=$((fails+1)); }
(cd "$work/repo" && "$PY" "$F" take slot:hyper "checked: host idle" --takeover >/dev/null) && grep -q "\"session\": \"$S12\"" "$FLEET_STATE/leases/slot__hyper.json" && echo "  ok    --takeover takes an orphaned resource with the reason recorded" || { echo "  FAIL  takeover failed: $(cd "$work/repo" && "$PY" "$F" take slot:hyper x --takeover 2>&1)"; fails=$((fails+1)); }
(cd "$work/repo" && "$PY" "$F" stop slot:hyper "host maintenance") >/dev/null
"$PY" -c "import json,sys; p=sys.argv[1]; r=json.load(open(p)); r['lane']={'kind':'liverun','requires':['slot:hyper'],'produces':'live'}; json.dump(r,open(p,'w'))" "$FLEET_STATE/sessions/$S12.json"
err=$(printf '%s' "$(tool PreToolUse $S12 $REPO Edit t68 "{\"file_path\":\"$REPO/b.ts\"}")" | "$PY" "$H" 2>&1 >/dev/null); rc=$?
case "$rc:$err" in 2:*"STAND DOWN on slot:hyper"*) echo "  ok    a stop on a resource stands its holder down at the next effectful call";; *) echo "  FAIL  slot stop: rc=$rc $err"; fails=$((fails+1));; esac
(cd "$work/repo" && "$PY" "$F" resume slot:hyper) >/dev/null
(cd "$work/repo" && "$PY" "$F" drop slot:hyper >/dev/null) && [ ! -e "$FLEET_STATE/leases/slot__hyper.json" ] && echo "  ok    the holder drops the resource and the lease is gone" || { echo "  FAIL  drop by holder"; fails=$((fails+1)); }
# Receipts (§5, §7.2): bound to the lane's produces, the session's own worktree, the exact HEAD, a clean tree.
"$PY" -c "import json,sys; p=sys.argv[1]; r=json.load(open(p)); r['pid_kind']='parent-unverified'; json.dump(r,open(p,'w'))" "$FLEET_STATE/sessions/$S11.json"
run "the liverun session is live again and touches its record"              0 "$(ev hook_event_name=UserPromptSubmit session_id=$S11 cwd=$WT2 prompt=hi)"
(cd "$work/wt2" && "$PY" "$F" take slot:hyper "packet" >/dev/null)
git -C "$work/wt2" -c user.email=t@t -c user.name=t commit -q --allow-empty -m "run head"
HEAD2=$(git -C "$work/wt2" rev-parse HEAD); SHA2=${HEAD2:0:10}
(cd "$work/wt2" && "$PY" "$F" receipt "$SHA2" live pass "gate blocked as expected" >/dev/null) && "$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if r['session']==sys.argv[2] and r['head']==sys.argv[3] and r['kind']=='live' and r['dirty'] is False else 1)" "$FLEET_STATE/receipts/$SHA2.live.json" "$S11" "$HEAD2" && echo "  ok    a receipt from the producing lane records session, full head and kind" || { echo "  FAIL  receipt: $(cd "$work/wt2" && "$PY" "$F" receipt "$SHA2" live pass x 2>&1)"; fails=$((fails+1)); }
out=$( (cd "$work/wt2" && "$PY" "$F" receipt "$SHA2" author pass "x") 2>&1 ); [ $? != 0 ] && case "$out" in *"produces"*) echo "  ok    a receipt of a kind the lane does not produce is refused";; *) echo "  FAIL  wrong-kind text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  wrong-kind receipt accepted"; fails=$((fails+1)); }
out=$( (cd "$work/wt" && "$PY" "$F" receipt "$SHA2" live pass "x") 2>&1 ); [ $? != 0 ] && echo "  ok    a lane that produces nothing cannot record a receipt" || { echo "  FAIL  a non-producing lane recorded a receipt"; fails=$((fails+1)); }
out=$( (cd "$work/wt2" && "$PY" "$F" receipt deadbeef00 live pass "x") 2>&1 ); [ $? != 0 ] && case "$out" in *"deadbeef00"*"$SHA2"*|*"deadbeef00"*"tree is at"*) echo "  ok    a receipt for a revision the tree is not at is refused, naming both";; *) echo "  FAIL  sha mismatch text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  receipt for the wrong revision accepted"; fails=$((fails+1)); }
printf 'dirt\n' > "$work/wt2/dirt.txt"
out=$( (cd "$work/wt2" && "$PY" "$F" receipt "$SHA2" live pass "x") 2>&1 ); [ $? != 0 ] && case "$out" in *"not clean"*) echo "  ok    a receipt from a dirty tree is refused";; *) echo "  FAIL  dirty text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  receipt from a dirty tree accepted"; fails=$((fails+1)); }
rm -f "$work/wt2/dirt.txt"
# --session disambiguates two live sessions in THIS directory; it is not a way to wear another
# session's lane. An ended lane's still-clean worktree would otherwise mint receipts for anyone.
"$PY" -c "import json,sys; p=sys.argv[1]; r=json.load(open(p)); r['ended']=True; json.dump(r,open(p,'w'))" "$FLEET_STATE/sessions/$S11.json"
out=$( (cd "$work/wt2" && "$PY" "$F" receipt "$SHA2" live pass "x" --session local_bbbb) 2>&1 ); rc=$?
[ "$rc" != 0 ] && case "$out" in *"not live"*) echo "  ok    --session cannot name an ended session";; *) echo "  FAIL  ended --session text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  a receipt was written for an ended session"; fails=$((fails+1)); }
"$PY" -c "import json,sys; p=sys.argv[1]; r=json.load(open(p)); r['ended']=False; json.dump(r,open(p,'w'))" "$FLEET_STATE/sessions/$S11.json"
out=$( (cd "$work/repo" && "$PY" "$F" receipt "$SHA2" live pass "x" --session local_bbbb) 2>&1 ); rc=$?
[ "$rc" != 0 ] && case "$out" in *"only disambiguates live sessions in this directory"*) echo "  ok    --session cannot borrow a session recorded in another directory";; *) echo "  FAIL  foreign --session text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  a receipt was written for a session in another directory"; fails=$((fails+1)); }
out=$( (cd "$work/wt" && "$PY" "$F" receipt "$SHA2" live pass "x" --session local_bbbb) 2>&1 ); [ $? != 0 ] && case "$out" in *"only disambiguates live sessions in this directory"*|*"roled worktree"*) echo "  ok    a receipt from outside the session's own worktree is refused";; *) echo "  FAIL  wrong-worktree text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  receipt from another worktree accepted"; fails=$((fails+1)); }
# The session ending never frees a resource (FR9); the operator's revoke does.
run "the liverun session ends"                                              0 "$(ev hook_event_name=SessionEnd session_id=$S11 cwd=$WT2 reason=exit)"
[ -e "$FLEET_STATE/leases/slot__hyper.json" ] && echo "  ok    SessionEnd leaves the resource lease in place (only drop or revoke free it)" || { echo "  FAIL  SessionEnd released a resource lease"; fails=$((fails+1)); }
(cd "$work/repo" && "$PY" "$F" revoke slot:hyper --to local_cccc "host handed over" >/dev/null) && grep -q "\"session\": \"$S12\"" "$FLEET_STATE/leases/slot__hyper.json" && echo "  ok    fleet revoke works on a resource key" || { echo "  FAIL  revoke on a slot"; fails=$((fails+1)); }
(cd "$work/repo" && "$PY" "$F" drop slot:hyper) >/dev/null
# Malformed and stray lease files (§8): refused by name, never free, never taken over.
KZ=$(ls "$FLEET_STATE/leases"/*__feat__z.json 2>/dev/null | head -1); [ -n "$KZ" ] || KZ="$FLEET_STATE/leases/$( "$PY" -c "import os,sys; os.environ['FLEET_STATE']=sys.argv[1]; sys.path.insert(0, os.environ['FLEET_HOOK_DIR']); import xlib as hook; print(hook.safe(hook.scope(sys.argv[2], 'feat/z')))" "$FLEET_STATE" "$WT2").json"
printf 'not json' > "$KZ"
run "the liverun session resumes"                                           0 "$(ev hook_event_name=SessionStart session_id=$S11 cwd=$WT2 source=resume)"
(cd "$work/wt2" && "$PY" "$F" take slot:hyper "packet" >/dev/null)
err=$(printf '%s' "$(tool PreToolUse $S11 $WT2 Edit t69 "{\"file_path\":\"$WT2/a.ts\"}")" | "$PY" "$H" 2>&1 >/dev/null); rc=$?
case "$rc:$err" in 2:*"malformed"*) echo "  ok    a malformed lease file refuses the write by name; it is not free";; *) echo "  FAIL  malformed lease: rc=$rc $err"; fails=$((fails+1));; esac
touch "$FLEET_STATE/leases/.tmp.slot__x.4242"
out=$("$PY" "$F" leases); case "$out" in *MALFORMED*"feat__z"*) case "$out" in *STRAY*".tmp.slot__x.4242"*) echo "  ok    fleet leases marks the malformed file and the stray temp";; *) echo "  FAIL  stray not listed: $out"; fails=$((fails+1));; esac;; *) echo "  FAIL  malformed not listed: $out"; fails=$((fails+1));; esac
rm -f "$KZ" "$FLEET_STATE/leases/.tmp.slot__x.4242"
(cd "$work/wt2" && "$PY" "$F" drop slot:hyper) >/dev/null
run "the liverun session ends for good"                                     0 "$(ev hook_event_name=SessionEnd session_id=$S11 cwd=$WT2 reason=exit)"

# ---------- Phase 2 (TDD §4.5, §6, §7.3) and the coordination views: slots, handoff, who, board, receipts, MCP ----------
# A pool goes BESIDE a checkout whose parent is outside every repo; $work/pools is that parent.
mkdir -p "$work/pools" && git init -q "$work/pools/repo3" && git -C "$work/pools/repo3" -c user.email=t@t -c user.name=t commit -q --allow-empty -m init \
  && git -C "$work/pools/repo3" branch feat/p && git -C "$work/pools/repo3" branch feat/q && git -C "$work/pools/repo3" branch feat/r
REPO3="$(native "$work/pools/repo3")"; SL1="$(native "$work/pools/repo3-finisher-1")"; SL2="$(native "$work/pools/repo3-finisher-2")"
printf '{"repo3": {"warm": "echo 1 >> %s", "slots": {"finisher": 2}}}\n' "$(native "$work/warmcount")" > "$FLEET_STATE/pools.json"
out=$(ORG_TENANT=work "$PY" "$F" pool "$REPO3" 2>&1); rc=$?   # no kind: counts come from pools.json
[ "$rc" = 0 ] && [ -d "$work/pools/repo3-finisher-1" ] && [ -d "$work/pools/repo3-finisher-2" ] && grep -q "repo3-finisher-1 work finisher:repo3 repo3-finisher-1$" "$ORG_STATE/roles.map" && echo "  ok    fleet pool <checkout> (kinds and counts from pools.json): two slots beside the checkout, roled <kind>:<repo>, slot name in the map's fourth column" || { echo "  FAIL  fleet pool: rc=$rc $out; map: $(grep repo3 "$ORG_STATE/roles.map")"; fails=$((fails+1)); }
rid3=$("$PY" -c "import os,sys; os.environ['FLEET_STATE']=sys.argv[1]; sys.path.insert(0, os.environ['FLEET_HOOK_DIR']); import xlib as hook; print(hook.repo_id(sys.argv[2]))" "$FLEET_STATE" "$SL1")
[ "$(wc -l < "$work/warmcount" | tr -d ' ')" = 2 ] && echo "  ok    fleet pool: the repo's warm command from pools.json ran once per new slot" || { echo "  FAIL  warm ran $(wc -l < "$work/warmcount" 2>/dev/null) times, wanted 2"; fails=$((fails+1)); }
out=$("$PY" "$F" slots repo3); case "$out" in *"repo3-finisher-1 "*free*"repo3-finisher-2 "*free*) echo "  ok    fleet slots: both slots free";; *) echo "  FAIL  fleet slots: $out"; fails=$((fails+1));; esac
n_before=$(grep -c repo3 "$ORG_STATE/roles.map")
out=$(ORG_TENANT=work "$PY" "$F" pool "$REPO3" finisher 2 2>&1)
[ "$n_before" = 2 ] && [ "$(grep -c repo3 "$ORG_STATE/roles.map")" = "$n_before" ] && case "$out" in *"kept repo3-finisher-1, repo3-finisher-2"*) echo "  ok    fleet pool is idempotent: re-running keeps both, adds no map line";; *) echo "  FAIL  pool re-run: $out"; fails=$((fails+1));; esac || { echo "  FAIL  pool re-run duplicated map lines"; fails=$((fails+1)); }
[ "$(wc -l < "$work/warmcount" | tr -d ' ')" = 2 ] && echo "  ok    a re-run does not re-warm a slot that already warmed" || { echo "  FAIL  re-run re-warmed"; fails=$((fails+1)); }
git init -q "$work/repo/nested-co" 2>/dev/null && git -C "$work/repo/nested-co" -c user.email=t@t -c user.name=t commit -q --allow-empty -m init
out=$(ORG_TENANT=work "$PY" "$F" pool "$(native "$work/repo/nested-co")" finisher 1 2>&1); rc=$?
[ "$rc" != 0 ] && [ ! -d "$work/repo/nested-co-finisher-1" ] && case "$out" in *nested*) echo "  ok    fleet pool refuses a checkout whose parent is inside a repo (slots would be nested)";; *) echo "  FAIL  nested refusal text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  nested pool accepted: rc=$rc"; fails=$((fails+1)); }
rm -rf "$work/repo/nested-co"
# `dirty` must see the INDEX: a staged file with no worktree diff read clean to `git diff --stat`.
printf 'staged\n' > "$work/pools/repo3-finisher-2/staged.txt" && git -C "$work/pools/repo3-finisher-2" add staged.txt
out=$("$PY" "$F" slots repo3); case "$out" in *"repo3-finisher-2 "*dirty*) echo "  ok    fleet slots: index != HEAD is dirty, not free";; *) echo "  FAIL  staged-only slot not dirty: $out"; fails=$((fails+1));; esac
git -C "$work/pools/repo3-finisher-2" reset -q && rm -f "$work/pools/repo3-finisher-2/staged.txt"
# Occupancy: SessionStart in a slot takes `slot:<name>`; that lease IS the name table `fleet who` reads.
S20=local_2020aaaa S21=local_2121bbbb
out=$("$PY" "$F" who repo3-finisher-1); rc=$?
[ "$rc" = 1 ] && case "$out" in *unoccupied*) echo "  ok    fleet who: an unoccupied slot resolves to nobody, exit 1";; *) echo "  FAIL  who text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  who resolved an empty slot (rc=$rc): $out"; fails=$((fails+1)); }
run "a session starts in slot 1"                                             0 "$(ev hook_event_name=SessionStart session_id=$S20 cwd=$SL1 source=startup)"
grep -q "\"session\": \"$S20\"" "$FLEET_STATE/leases/slot__repo3-finisher-1.json" 2>/dev/null && grep -q '"occupancy": true' "$FLEET_STATE/leases/slot__repo3-finisher-1.json" && echo "  ok    SessionStart in a slot writes the occupancy lease slot:<name> for the session" || { echo "  FAIL  no occupancy lease: $(ls "$FLEET_STATE/leases")"; fails=$((fails+1)); }
out=$("$PY" "$F" who repo3-finisher-1); rc=$?
[ "$rc" = 0 ] && case "$out" in *"$S20"*) echo "  ok    fleet who <slot> resolves to the live session there, exit 0";; *) echo "  FAIL  who: $out"; fails=$((fails+1));; esac || { echo "  FAIL  who rc=$rc: $out"; fails=$((fails+1)); }
out=$("$PY" "$F" slots repo3); case "$out" in *"repo3-finisher-1 "*"busy(local_20"*) echo "  ok    fleet slots: busy(<sid8>, <branch>) while the session is live";; *) echo "  FAIL  slots busy: $out"; fails=$((fails+1));; esac
out=$("$PY" "$F" board); case "$out" in *"idle "*"finisher:repo3"*"repo3-finisher-1"*"local_20"*) echo "  ok    fleet board: a live session with its turn closed and no work held is idle (the seat itself is not work)";; *) echo "  FAIL  board idle: $out"; fails=$((fails+1));; esac
out=$(printf '%s' "$(ev hook_event_name=SessionStart session_id=$S21 cwd=$SL1 source=startup)" | "$PY" "$H")
case "$out" in *"already occupied by"*"local_20"*) echo "  ok    a second session in the same slot is told the seat is occupied, naming the occupant";; *) echo "  FAIL  second occupant not warned: $out"; fails=$((fails+1));; esac
grep -q "\"session\": \"$S20\"" "$FLEET_STATE/leases/slot__repo3-finisher-1.json" && echo "  ok    and the live occupant keeps the seat" || { echo "  FAIL  a live occupant was displaced at SessionStart"; fails=$((fails+1)); }
run "the second session ends"                                                0 "$(ev hook_event_name=SessionEnd session_id=$S21 cwd=$SL1 reason=exit)"
# Never a substitute: with slot 2 live and slot 1's occupant dead, `who slot-1` must name nobody, not slot 2's session.
run "a session starts in slot 2"                                             0 "$(ev hook_event_name=SessionStart session_id=$S21 cwd=$SL2 source=startup)"
"$PY" -c "import json,sys; p=sys.argv[1]; r=json.load(open(p)); r['pid']=999999; r['pid_kind']='harness'; json.dump(r,open(p,'w'))" "$FLEET_STATE/sessions/$S20.json"
out=$("$PY" "$F" who repo3-finisher-1); rc=$?
[ "$rc" = 1 ] && case "$out" in *"$S21"*) echo "  FAIL  who substituted another live session for a dead occupant: $out"; fails=$((fails+1));; *unoccupied*"local_20"*) echo "  ok    fleet who: a dead occupant resolves to nobody and names the dead session, never a substitute";; *) echo "  FAIL  who dead text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  who resolved a dead occupant (rc=$rc): $out"; fails=$((fails+1)); }
out=$("$PY" "$F" slots repo3); case "$out" in *"repo3-finisher-1 "*"orphaned(local_20"*) echo "  ok    fleet slots: a dead occupant's slot is orphaned(<sid8>)";; *) echo "  FAIL  slots orphaned: $out"; fails=$((fails+1));; esac
out=$("$PY" "$F" board); case "$out" in *"dead "*"repo3-finisher-1"*) echo "  ok    fleet board: a dead pid is dead, whatever the lane promised";; *) echo "  FAIL  board dead: $out"; fails=$((fails+1));; esac
out=$("$PY" "$F" who no-such-slot); rc=$?
[ "$rc" = 1 ] && case "$out" in *"repo3-finisher-1"*) echo "  ok    fleet who: an unknown name fails loudly and lists the slots that exist";; *) echo "  FAIL  unknown-name text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  unknown name resolved"; fails=$((fails+1)); }
# A dead occupant is displaced by the next SessionStart there (a worktree cannot still be running).
S23=local_2323dddd
run "a new session starts in slot 1 over the dead occupant"                  0 "$(ev hook_event_name=SessionStart session_id=$S23 cwd=$SL1 source=startup)"
grep -q "\"session\": \"$S23\"" "$FLEET_STATE/leases/slot__repo3-finisher-1.json" && echo "  ok    a dead occupant's seat is taken at the next SessionStart without ceremony" || { echo "  FAIL  seat not retaken: $(cat "$FLEET_STATE/leases/slot__repo3-finisher-1.json")"; fails=$((fails+1)); }
run "that session ends"                                                      0 "$(ev hook_event_name=SessionEnd session_id=$S23 cwd=$SL1 reason=exit)"
"$PY" -c "import json,sys; p=sys.argv[1]; r=json.load(open(p)); r['pid_kind']='parent-unverified'; json.dump(r,open(p,'w'))" "$FLEET_STATE/sessions/$S20.json"
run "the first session resumes in slot 1 and retakes the free seat"          0 "$(ev hook_event_name=SessionStart session_id=$S20 cwd=$SL1 source=resume)"
# pool never disturbs an occupied slot: --rewarm reaches only the free one.
out=$(ORG_TENANT=work "$PY" "$F" pool "$REPO3" finisher 2 --rewarm 2>&1)
run "slot 2's session ends"                                                  0 "$(ev hook_event_name=SessionEnd session_id=$S21 cwd=$SL2 reason=exit)"
[ "$(wc -l < "$work/warmcount" | tr -d ' ')" = 2 ] && case "$out" in *"kept repo3-finisher-1, repo3-finisher-2"*) echo "  ok    --rewarm skips every slot with a live session in it (both were occupied)";; *) echo "  FAIL  rewarm output: $out"; fails=$((fails+1));; esac || { echo "  FAIL  --rewarm touched an occupied slot: $(wc -l < "$work/warmcount") warms"; fails=$((fails+1)); }
out=$(ORG_TENANT=work "$PY" "$F" pool "$REPO3" finisher 2 --rewarm 2>&1)
[ "$(wc -l < "$work/warmcount" | tr -d ' ')" = 3 ] && echo "  ok    --rewarm re-warms the slot that is free and leaves the occupied one alone" || { echo "  FAIL  rewarm count $(wc -l < "$work/warmcount"): $out"; fails=$((fails+1)); }
[ ! -e "$FLEET_STATE/leases/slot__repo3-finisher-2.json" ] && echo "  ok    SessionEnd releases the occupancy lease (the session was the occupant)" || { echo "  FAIL  occupancy lease outlived its session"; fails=$((fails+1)); }
# Branch-switch handoff (§7.3, FR6): the destination is leased BEFORE git runs, the origin kept, and
# HEAD decides at the next hook which one is released. Over-hold, never under-hold.
git -C "$work/pools/repo3-finisher-1" checkout -q feat/p
run "slot 1 writes on feat/p and takes its lease"                            0 "$(tool PreToolUse $S20 $SL1 Edit t80 "{\"file_path\":\"$SL1/a.ts\"}")"
run "'git checkout feat/q' leases feat/q first and records the handoff"       0 "$(tool PreToolUse $S20 $SL1 Bash t81 '{"command":"git checkout feat/q"}')"
lp="$FLEET_STATE/leases/repo__${rid3}__feat__p.json"; lq="$FLEET_STATE/leases/repo__${rid3}__feat__q.json"
[ -e "$lp" ] || lp=""; [ -e "$lq" ] || lq=""
[ -n "$lp" ] && [ -n "$lq" ] && grep -q "$S20" "$lq" && "$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); h=r.get('handoff') or {}; sys.exit(0 if h.get('from') and any(t.endswith(':feat/q') for t in h.get('to') or []) else 1)" "$FLEET_STATE/sessions/$S20.json" && echo "  ok    both leases held and handoff{from,to} on the session while the switch is in flight" || { echo "  FAIL  handoff state: p=$lp q=$lq $(cat "$FLEET_STATE/sessions/$S20.json")"; fails=$((fails+1)); }
git -C "$work/pools/repo3-finisher-1" checkout -q feat/q          # the switch lands
run "the next call settles the handoff from HEAD"                            0 "$(tool PreToolUse $S20 $SL1 Bash t82 '{"command":"git status"}')"
[ ! -e "$FLEET_STATE/leases/repo__${rid3}__feat__p.json" ] && [ -e "$lq" ] && "$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if not r.get('handoff') else 1)" "$FLEET_STATE/sessions/$S20.json" && echo "  ok    landed on feat/q: feat/p released, feat/q kept, handoff cleared" || { echo "  FAIL  settle after success: p=$([ -e "$lp" ] && echo held || echo free) q=$([ -e "$lq" ] && echo held || echo free)"; fails=$((fails+1)); }
run "'git checkout feat/r' leases feat/r (the switch will fail)"              0 "$(tool PreToolUse $S20 $SL1 Bash t83 '{"command":"git checkout feat/r"}')"
lr="$FLEET_STATE/leases/repo__${rid3}__feat__r.json"
[ -e "$lr" ] && echo "  ok    the destination feat/r is leased before git runs" || { echo "  FAIL  destination not leased: $(ls "$FLEET_STATE/leases")"; fails=$((fails+1)); }
run "the switch did not land; the next call settles from HEAD"               0 "$(tool PreToolUse $S20 $SL1 Bash t84 '{"command":"git status"}')"
[ ! -e "$lr" ] && [ -e "$lq" ] && echo "  ok    a failed switch drops the destination lease and keeps the origin; nothing was ever free that was not free" || { echo "  FAIL  settle after failure: r=$(ls "$FLEET_STATE/leases"/*__feat__r.json 2>/dev/null) q=$([ -e "$lq" ] && echo held || echo free)"; fails=$((fails+1)); }
run "'git checkout feat/r -- a.ts' is a path restore, not a switch"           0 "$(tool PreToolUse $S20 $SL1 Bash t85 '{"command":"git checkout feat/r -- a.ts"}')"
"$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if not r.get('handoff') else 1)" "$FLEET_STATE/sessions/$S20.json" && [ ! -e "$lr" ] && echo "  ok    a path restore records no handoff and leases no destination" || { echo "  FAIL  path restore recorded a handoff"; fails=$((fails+1)); }
# Held destination: another live session holds feat/r -> the switch is refused and nothing moves.
S22=local_2222cccc
"$PY" - "$FLEET_STATE" "$REPO3" <<'PY'
import json, os, sys
os.environ["FLEET_STATE"] = sys.argv[1]; sys.path.insert(0, os.environ["FLEET_HOOK_DIR"]); import xlib as hook
hook.write_json(hook.path("sessions", "local_2222cccc.json"), {"session": "local_2222cccc", "cwd": sys.argv[2], "pid_kind": "parent-unverified", "last_event_at": hook.now(), "role": "r"})
key = hook.scope(sys.argv[2], "feat/r")
hook.take_lease(key, "feat/r", "local_2222cccc", "r", sys.argv[2], "held elsewhere")
PY
run "'git checkout feat/r' while a live session holds feat/r → denied"        2 "$(tool PreToolUse $S20 $SL1 Bash t86 '{"command":"git checkout feat/r"}')"
"$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if not r.get('handoff') else 1)" "$FLEET_STATE/sessions/$S20.json" && [ -e "$lq" ] && echo "  ok    a refused switch records no handoff and the origin stays held" || { echo "  FAIL  refused switch left state behind"; fails=$((fails+1)); }
# A settle that cannot complete must not reach main's catch-all (exit 0 = the lease check skipped).
# The destination key's lock is held by another process; the session has a handoff in flight and
# tries to write on a branch a live rival holds. Denied, and the handoff stays in flight.
"$PY" - "$FLEET_STATE" "$SL1" "$H" "$S20" <<'PY' && echo "  ok    a settle blocked by a busy lock leaves the handoff in flight and the lease verdict still runs (denied, not allowed)" || { echo "  FAIL  a KeyBusy inside settle_handoff became an allow"; fails=$((fails+1)); }
import json, os, subprocess, sys, time
state, sl1, hookpy, sid = sys.argv[1:5]
os.environ["FLEET_STATE"] = state; sys.path.insert(0, os.environ["FLEET_HOOK_DIR"]); import xlib as hook
rec_p = hook.path("sessions", f"{sid}.json"); rec = hook.read_json(rec_p)
saved = dict(rec)
rival_key = hook.scope(sl1, "feat/p")                       # the branch this session will try to write
hook.write_json(hook.path("sessions", "rivalp.json"), {"session": "rivalp", "cwd": "/elsewhere", "pid_kind": "parent-unverified", "last_event_at": hook.now(), "role": "r"})
hook.take_lease(rival_key, "feat/p", "rivalp", "r", "/elsewhere", "held by a live rival")
dest = hook.scope(sl1, "feat/r")
rec["handoff"] = {"from": hook.scope(sl1, "feat/q"), "to": dest, "to_branch": "feat/r", "start": sl1, "at": hook.now()}
hook.write_json(rec_p, rec)
holder = subprocess.Popen([sys.executable, "-c", "import os,sys,time; os.environ['FLEET_STATE']=sys.argv[1]; sys.path.insert(0, sys.argv[2]); import xlib as hook\nwith hook.keylock(sys.argv[3]):\n    time.sleep(4)", state, os.environ["FLEET_HOOK_DIR"], dest])
time.sleep(0.5)
ev = {"hook_event_name": "PreToolUse", "session_id": sid, "cwd": sl1, "tool_name": "Bash", "tool_use_id": "t89",
      "tool_input": {"command": "git -C " + sl1 + " commit -m x"}}
# the write targets feat/p: point the tree there so the verdict is about the rival's branch
subprocess.run(["git", "-C", sl1, "checkout", "-q", "feat/p"])
r = subprocess.run([sys.executable, hookpy], input=json.dumps(ev), capture_output=True, text=True)
holder.kill(); holder.wait()
after = hook.read_json(rec_p) or {}
ok = r.returncode == 2 and "held by" in r.stderr and isinstance(after.get("handoff"), dict)
if not ok:
    print("    rc:", r.returncode, "stderr:", r.stderr.strip()[:160], "handoff after:", after.get("handoff"))
# restore: tree back on feat/q, rival gone, handoff cleared
subprocess.run(["git", "-C", sl1, "checkout", "-q", "feat/q"])
hook.drop_lease(rival_key, "rivalp"); hook._unlink(hook.path("sessions", "rivalp.json"))
saved["handoff"] = None; hook.write_json(rec_p, saved)
sys.exit(0 if ok else 1)
PY
# fleet assign: dispatch places work into a FREE slot and records it; the slot's next SessionStart reads it.
out=$("$PY" "$F" assign repo3-finisher-1 feat/p "fix the flake" 2>&1); rc=$?
[ "$rc" != 0 ] && case "$out" in *busy*) echo "  ok    fleet assign refuses a busy slot";; *) echo "  FAIL  assign busy text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  assign into a busy slot accepted"; fails=$((fails+1)); }
out=$("$PY" "$F" assign repo3-finisher-2 feat/r "fix the flake" 2>&1); rc=$?
[ "$rc" != 0 ] && case "$out" in *"held by"*"local_22"*) echo "  ok    fleet assign refuses a branch a live session holds (two sessions on one branch)";; *) echo "  FAIL  assign held-branch text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  assign onto a held branch accepted"; fails=$((fails+1)); }
out=$("$PY" "$F" assign repo3-finisher-2 feat/p "fix the flake" 2>&1); rc=$?
[ "$rc" = 0 ] && [ "$(git -C "$work/pools/repo3-finisher-2" rev-parse --abbrev-ref HEAD)" = "feat/p" ] && [ -e "$FLEET_STATE/assign/repo3-finisher-2.json" ] && echo "  ok    fleet assign checks the branch out in the free slot and records the assignment" || { echo "  FAIL  assign: rc=$rc $out"; fails=$((fails+1)); }
"$PY" - "$FLEET_STATE" "$SL2" "$F" <<'PY' && echo "  ok    fleet assign re-checks under the seat's lock: a slot occupied while assign held the lock is refused and its tree is not moved" || { echo "  FAIL  assign moved a worktree a session had just occupied"; fails=$((fails+1)); }
import json, os, subprocess, sys, time
state, sl2, fleetpy = sys.argv[1:4]
os.environ["FLEET_STATE"] = state; sys.path.insert(0, os.environ["FLEET_HOOK_DIR"]); import xlib as hook
before = subprocess.run(["git", "-C", sl2, "rev-parse", "--abbrev-ref", "HEAD"], capture_output=True, text=True).stdout.strip()
# Hold the seat's lock as an occupying SessionStart would; the session record lands while the lock is
# held. An assign that decides under the lock waits and then sees the record; one that decides
# outside it reads "free", checks out, and moves the tree under the session that just sat down.
occ = subprocess.Popen([sys.executable, "-c", "import os,sys,time; os.environ['FLEET_STATE']=sys.argv[1]; sys.path.insert(0, sys.argv[2]); import xlib as hook\nwith hook.keylock('slot:repo3-finisher-2'):\n    time.sleep(0.6)\n    hook.write_json(hook.path('sessions','occ_late.json'), {'session':'occ_late','cwd':sys.argv[3],'pid_kind':'parent-unverified','last_event_at':hook.now(),'role':'r'})\n    time.sleep(0.3)", state, os.environ["FLEET_HOOK_DIR"], sl2])
time.sleep(0.2)
r = subprocess.run([sys.executable, fleetpy, "assign", "repo3-finisher-2", "feat/q", "late"], capture_output=True, text=True)
occ.wait()
after = subprocess.run(["git", "-C", sl2, "rev-parse", "--abbrev-ref", "HEAD"], capture_output=True, text=True).stdout.strip()
rec = hook.read_json(hook.path("assign", "repo3-finisher-2.json")) or {}
ok = r.returncode != 0 and "busy" in (r.stderr + r.stdout) and after == before and rec.get("branch") != "feat/q"
if not ok:
    print("    rc:", r.returncode, (r.stderr + r.stdout).strip()[:200], "| HEAD before/after:", before, after)
hook._unlink(hook.path("sessions", "occ_late.json"))
sys.exit(0 if ok else 1)
PY
out=$("$PY" "$F" slots repo3); case "$out" in *"repo3-finisher-2 "*"assigned(feat/p)"*) echo "  ok    fleet slots shows the free slot as assigned(<branch>)";; *) echo "  FAIL  slots assigned: $out"; fails=$((fails+1));; esac
out=$(printf '%s' "$(ev hook_event_name=SessionStart session_id=$S21 cwd=$SL2 source=startup)" | "$PY" "$H")
case "$out" in *"assigned"*"feat/p"*"fix the flake"*) echo "  ok    the session that opens in the slot is told its assignment at SessionStart";; *) echo "  FAIL  assignment not injected: $out"; fails=$((fails+1));; esac
out=$("$PY" "$F" slots repo3); case "$out" in *"assigned(feat/p)"*) echo "  FAIL  a delivered assignment still shows on fleet slots: $out"; fails=$((fails+1));; *) echo "  ok    once a session has been told, the assignment is spent: fleet slots no longer shows it";; esac
"$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if r.get('delivered_to')==sys.argv[2] else 1)" "$FLEET_STATE/assign/repo3-finisher-2.json" "$S21" && echo "  ok    the record names the session it was delivered to" || { echo "  FAIL  delivery not stamped: $(cat "$FLEET_STATE/assign/repo3-finisher-2.json")"; fails=$((fails+1)); }
"$PY" "$F" unassign repo3-finisher-2 >/dev/null && [ ! -e "$FLEET_STATE/assign/repo3-finisher-2.json" ] && echo "  ok    fleet unassign clears it" || { echo "  FAIL  unassign"; fails=$((fails+1)); }
# Change-number -> branch is derived from `gh pr` calls the session ran anyway, never declared.
ghresp() { "$PY" -c "import json,sys; print(json.dumps({'hook_event_name':'PostToolUse','session_id':sys.argv[1],'cwd':sys.argv[2],'tool_name':'Bash','tool_use_id':sys.argv[3],'tool_input':{'command':sys.argv[4]},'tool_response':{'stdout':sys.argv[5],'stderr':''}}))" "$@"; }
run "PostToolUse of 'gh pr create' with the URL in its output"                0 "$(ghresp $S20 $SL1 t87 'gh pr create --fill' 'https://github.com/acme/repo3/pull/55')"
"$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if r.get('branch')=='feat/q' and r.get('number')==55 and r.get('github')=='acme/repo3' else 1)" "$FLEET_STATE/prs/${rid3}__55.json" 2>/dev/null && echo "  ok    prs/<repo>__55.json records branch feat/q (the tree's branch at the time) and the GitHub repo" || { echo "  FAIL  change cache: $(ls "$FLEET_STATE/prs" 2>/dev/null; cat "$FLEET_STATE/prs/${rid3}__55.json" 2>/dev/null)"; fails=$((fails+1)); }
run "PostToolUse of 'gh pr view 77' (another change; this tree says nothing about its branch)" 0 "$(ghresp $S20 $SL1 t88 'gh pr view 77' 'https://github.com/acme/repo3/pull/77 some title')"
[ ! -e "$FLEET_STATE/prs/${rid3}__77.json" ] && echo "  ok    a view of some other change records nothing (a miss is filled by gh later, never guessed)" || { echo "  FAIL  guessed a branch for #77: $(cat "$FLEET_STATE/prs/${rid3}__77.json")"; fails=$((fails+1)); }
out=$( (cd "$work/pools/repo3-finisher-1" && "$PY" "$F" who 55) 2>&1 ); rc=$?
[ "$rc" = 0 ] && case "$out" in *"feat/q"*"$S20"*) echo "  ok    fleet who #55 -> the session holding its head branch's lease (cache, then lease; no network)";; *) echo "  FAIL  who #55: $out"; fails=$((fails+1));; esac || { echo "  FAIL  who #55 rc=$rc: $out"; fails=$((fails+1)); }
out=$( (cd "$work/pools/repo3-finisher-1" && "$PY" "$F" done 55) 2>&1 ); rc=$?
[ "$rc" = 1 ] && case "$out" in *"NOT DONE"*"feat/q"*) echo "  ok    fleet done #55 resolves the head through the cache and exits 1 with no receipt";; *) echo "  FAIL  done #55: $out"; fails=$((fails+1));; esac || { echo "  FAIL  done #55 rc=$rc: $out"; fails=$((fails+1)); }
out=$( (cd "$work/pools/repo3-finisher-1" && "$PY" "$F" done no/such/branch) 2>&1 ); rc=$?
[ "$rc" = 2 ] && echo "  ok    fleet done exits 2 when the revision cannot be resolved (distinct from not-done)" || { echo "  FAIL  unresolvable done rc=$rc: $out"; fails=$((fails+1)); }
# Receipts read path: done is a fact the reader polls; the card is the evidence a person opens.
out=$( (cd "$work/wt2" && "$PY" "$F" done "$SHA2") 2>&1 ); rc=$?
[ "$rc" = 0 ] && case "$out" in *"DONE"*"live pass"*) echo "  ok    fleet done <sha> exits 0 on the passing live receipt from phase 1";; *) echo "  FAIL  done text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  done rc=$rc: $out"; fails=$((fails+1)); }
out=$( (cd "$work/wt2" && "$PY" "$F" done "$SHA2" --kind other) 2>&1 ); rc=$?
[ "$rc" = 1 ] && echo "  ok    fleet done --kind <k> exits 1 when no receipt of that kind exists, whatever else passed" || { echo "  FAIL  done --kind other rc=$rc"; fails=$((fails+1)); }
run "the liverun session is live again"                                     0 "$(ev hook_event_name=SessionStart session_id=$S11 cwd=$WT2 source=resume)"
(cd "$work/wt2" && "$PY" "$F" take slot:hyper "card run" >/dev/null)
git -C "$work/wt2" -c user.email=t@t -c user.name=t commit -q --allow-empty -m "card run"
HEAD3=$(git -C "$work/wt2" rev-parse HEAD); SHA3=${HEAD3:0:10}
out=$( (cd "$work/wt2" && "$PY" "$F" receipt "$SHA3" live fail "guard did not fire on the reverted bug" --card not-a-url) 2>&1 ); [ $? != 0 ] && [ ! -e "$FLEET_STATE/receipts/$SHA3.live.json" ] && echo "  ok    --card must be a URL; nothing written otherwise" || { echo "  FAIL  bad --card accepted: $out"; fails=$((fails+1)); }
(cd "$work/wt2" && "$PY" "$F" receipt "$SHA3" live fail "guard did not fire on the reverted bug" --card https://jira.example/browse/X-1 >/dev/null) || { echo "  FAIL  receipt with --card refused"; fails=$((fails+1)); }
out=$( (cd "$work/wt2" && "$PY" "$F" done "$SHA3") 2>&1 ); rc=$?
[ "$rc" = 3 ] && case "$out" in *FAILED*"jira.example/browse/X-1"*) echo "  ok    a failing receipt is exit 3 (evidence of failure, distinct from 1 = still pending) and fleet done surfaces the card URL";; *) echo "  FAIL  done fail text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  done on a fail receipt rc=$rc (wanted 3)"; fails=$((fails+1)); }
out=$("$PY" "$F" receipts --kind live); case "$out" in *"$SHA3"*fail*"$SHA2"*pass*) echo "  ok    fleet receipts lists newest first, filtered by kind";; *) echo "  FAIL  receipts list: $out"; fails=$((fails+1));; esac
(cd "$work/wt2" && "$PY" "$F" drop slot:hyper) >/dev/null
run "the liverun session ends"                                              0 "$(ev hook_event_name=SessionEnd session_id=$S11 cwd=$WT2 reason=exit)"
# The board's four states from synthetic records against roled paths; cadence comes from the lane.
"$PY" - "$FLEET_STATE" "$REPO3" "$SL2" <<'PY' && echo "  ok    fleet board: idle-holding-work, busy, busy-and-overdue (past cadence) and vacant each come from the record that means it" || { echo "  FAIL  board states"; fails=$((fails+1)); }
import json, os, subprocess, sys
os.environ["FLEET_STATE"] = sys.argv[1]; sys.path.insert(0, os.environ["FLEET_HOOK_DIR"]); import xlib as hook; fleet = hook
repo3, sl2 = sys.argv[2], sys.argv[3]
def sess(sid, cwd, **kw):
    hook.write_json(hook.path("sessions", f"{sid}.json"), {"session": sid, "cwd": cwd, "pid_kind": "parent-unverified", "last_event_at": hook.now(), "role": "finisher:repo3", **kw})
# slot 2: idle, turn closed, holding a branch lease -> idle-holding-work
sess("bd_idlework", sl2, turn_open=False, lane={"kind": "finisher", "requires": [], "produces": None, "cadence": 3600})
hook.take_lease(hook.scope(sl2, "feat/p"), "feat/p", "bd_idlework", "finisher:repo3", sl2, "held")
rows = {r["slot"] or r["path"]: r for r in fleet.board_rows()}
bad = []
if rows["repo3-finisher-2"]["state"] != "idle-holding-work": bad.append(("slot2 idle-holding-work", rows["repo3-finisher-2"]["state"]))
# same session busy, turn opened just now -> busy
sess("bd_idlework", sl2, turn_open=True, turn_open_at=hook.now(), lane={"kind": "finisher", "requires": [], "produces": None, "cadence": 3600})
if {r["slot"]: r for r in fleet.board_rows()}["repo3-finisher-2"]["state"] != "busy": bad.append(("busy", "not busy"))
# turn open for longer than the lane's cadence -> busy-and-overdue
sess("bd_idlework", sl2, turn_open=True, turn_open_at=hook.now() - 7200, lane={"kind": "finisher", "requires": [], "produces": None, "cadence": 3600})
if {r["slot"]: r for r in fleet.board_rows()}["repo3-finisher-2"]["state"] != "busy-and-overdue": bad.append(("overdue", "not overdue"))
# no cadence in the lane -> never overdue, only busy
sess("bd_idlework", sl2, turn_open=True, turn_open_at=hook.now() - 7200, lane={"kind": "finisher", "requires": [], "produces": None, "cadence": None})
if {r["slot"]: r for r in fleet.board_rows()}["repo3-finisher-2"]["state"] != "busy": bad.append(("no cadence", "overdue without a cadence"))
# a roled path nobody has ever started in -> vacant
if not any(r["state"] == "vacant" for r in fleet.board_rows()): bad.append(("vacant", "no vacant row"))
for b in bad: print("   ", b)
hook.drop_lease(hook.scope(sl2, "feat/p"), "bd_idlework"); hook._unlink(hook.path("sessions", "bd_idlework.json"))
sys.exit(1 if bad else 0)
PY
# The shipped live-run lane: two keys (the machine and the operator's attention), a receipt kind, a cadence.
"$PY" -c "import json,sys; m=json.load(open(sys.argv[1])); sys.exit(0 if m['produces']=='live' and set(m['requires'])=={'slot:hypermill','slot:live-run'} and m.get('cadence') else 1)" "$here/lanes/liverun/manifest.json" && echo "  ok    lanes/liverun requires slot:hypermill AND slot:live-run, produces live, states a cadence" || { echo "  FAIL  shipped liverun manifest"; fails=$((fails+1)); }
mkdir -p "$work/lanes-bad/cad"; printf '{"kind":"cad","card":"card.md","denies":[],"requires":[],"produces":null,"slots":0,"cadence":"soon"}\n' > "$work/lanes-bad/cad/manifest.json"; printf 'card\n' > "$work/lanes-bad/cad/card.md"
out=$(FLEET_LANES="$work/lanes-bad" "$PY" "$F" role "$REPO" cad:cam 2>&1); rc=$?
[ "$rc" != 0 ] && case "$out" in *cadence*) echo "  ok    a manifest cadence that is not a duration is refused, naming the key";; *) echo "  FAIL  bad cadence text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  bad cadence accepted"; fails=$((fails+1)); }
"$PY" "$F" role "$REPO" supervisor:cam >/dev/null 2>&1
# MCP: the same functions over stdio. Eight tools, one-line descriptions, refusals verbatim.
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}' '{"jsonrpc":"2.0","method":"notifications/initialized"}' '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fleet_who","arguments":{"name":"repo3-finisher-1"}}}' \
  '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"fleet_assign","arguments":{"slot":"repo3-finisher-1","branch":"feat/p"}}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":5,\"method\":\"tools/call\",\"params\":{\"name\":\"fleet_done\",\"arguments\":{\"revision\":\"$SHA3\"}}}" \
  '{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"fleet_assign","arguments":{"slot":"repo3-finisher-1"}}}' \
  '{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"fleet_take","arguments":{"resource":"slot:hyper"}}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":8,\"method\":\"tools/call\",\"params\":{\"name\":\"fleet_who\",\"arguments\":{\"name\":\"feat/q\",\"cwd\":\"$SL1\"}}}" \
  | (cd "$work" && "$PY" "$here/fleet-mcp.py") > "$work/mcp.out" 2>"$work/mcp.err"
"$PY" - "$work/mcp.out" "$S20" <<'PY' && echo "  ok    fleet-mcp: initialize, 8 one-line tools, who resolves in the caller's cwd, refusal is isError with the CLI's text, not-done is an answer, missing arg is -32602, acting tools need cwd" || { echo "  FAIL  fleet-mcp: $(cat "$work/mcp.out" "$work/mcp.err")"; fails=$((fails+1)); }
import json, sys
by = {}
for line in open(sys.argv[1], encoding="utf-8"):
    d = json.loads(line); by[d["id"]] = d
bad = []
if by[1]["result"]["serverInfo"]["name"] != "fleet": bad.append("initialize")
tools = by[2]["result"]["tools"]
if len(tools) != 8 or any("\n" in t["description"] or len(t["description"]) > 160 for t in tools): bad.append("tools: %d, long or multi-line description" % len(tools))
if sys.argv[2] not in by[3]["result"]["content"][0]["text"] or by[3]["result"]["isError"]: bad.append("who")
if not by[4]["result"]["isError"] or "busy" not in by[4]["result"]["content"][0]["text"]: bad.append("assign refusal")
if by[5]["result"]["isError"] or '"ok": false' not in by[5]["result"]["content"][0]["text"]: bad.append("done")
if "error" not in by[6] or by[6]["error"]["code"] != -32602 or "branch" not in by[6]["error"]["message"] or "unknown tool" in by[6]["error"]["message"]: bad.append("missing argument must be -32602 naming it, not unknown tool: %s" % by[6])
if "error" not in by[7] or "cwd" not in by[7]["error"]["message"]: bad.append("an acting tool without cwd must be refused naming cwd: %s" % by[7])
if by[8]["result"]["isError"] or sys.argv[2] not in by[8]["result"]["content"][0]["text"]: bad.append("who with cwd must resolve the branch in the CALLER's repo, not the server's: %s" % by[8])
for b in bad: print("   ", b)
sys.exit(1 if bad else 0)
PY
# ---------- Panel findings on the phase 2 head (six independent reviewers), each with the scenario that reddens alone ----------
# switch_targets: forms that ARE a switch and were missed, and forms that are not and were treated as one.
git -C "$work/pools/repo3" branch feat/remote-only >/dev/null 2>&1
mkdir -p "$work/pools/repo3/.git/refs/remotes/origin/feat" && cp "$work/pools/repo3/.git/refs/heads/feat/remote-only" "$work/pools/repo3/.git/refs/remotes/origin/feat/remote-only" && git -C "$work/pools/repo3" branch -D feat/remote-only -q
"$PY" - "$FLEET_STATE" "$SL1" <<'PY' && echo "  ok    switch_targets: remote-only, -t origin/x, quoted, -b, compound a && b are switches; --detach, origin/x, <branch> <path>, -- are not" || { echo "  FAIL  switch detection"; fails=$((fails+1)); }
import os, sys
os.environ["FLEET_STATE"] = sys.argv[1]; sys.path.insert(0, os.environ["FLEET_HOOK_DIR"]); import xlib as hook
sl1 = sys.argv[2]
cases = {
    "git checkout feat/remote-only": ["feat/remote-only"],            # exists only on origin: git creates and switches
    "git checkout -t origin/feat/remote-only": ["feat/remote-only"],  # tracking form lands on the local name
    "git switch --track origin/feat/remote-only": ["feat/remote-only"],
    'git checkout "feat/q"': ["feat/q"],                              # quoted
    "git checkout -b feat/brand-new": ["feat/brand-new"],
    "git checkout feat/p && git checkout feat/q": ["feat/p", "feat/q"],  # both destinations, in order
    "git checkout --detach feat/q": [],
    "git switch --detach feat/q": [],
    "git checkout origin/feat/remote-only": [],                       # remote ref: detached
    "git checkout feat/q a.ts": [],                                    # branch + path = restore
    "git checkout feat/r -- a.ts": [],
    "git switch -": [],
    "git checkout 0123456789abcdef0123456789abcdef01234567": [],
    "git log feat/q": [],
}
bad = [(c, hook.switch_targets(c, sl1), want) for c, want in cases.items() if hook.switch_targets(c, sl1) != want]
for c, got, want in bad:
    print(f"    {c!r}: got {got}, wanted {want}")
sys.exit(1 if bad else 0)
PY
# A compound switch leases BOTH destinations before git runs; the settle keeps the one HEAD is on.
run "'git checkout feat/p && git checkout feat/remote-only' leases both"       0 "$(tool PreToolUse $S20 $SL1 Bash t90 '{"command":"git checkout feat/p && git checkout feat/remote-only"}')"
[ -e "$FLEET_STATE/leases/repo__${rid3}__feat__p.json" ] && [ -e "$FLEET_STATE/leases/repo__${rid3}__feat__remote-only.json" ] && echo "  ok    both destinations are held while the compound switch is in flight (one of them exists only on origin)" || { echo "  FAIL  compound switch did not lease both: $(ls "$FLEET_STATE/leases")"; fails=$((fails+1)); }
# feat/p is checked out in slot 2 so git refuses it, and with no remote configured git's DWIM will not
# create the tracking branch for us: land on the second destination by hand, as git would have.
git -C "$work/pools/repo3-finisher-1" checkout -q -b feat/remote-only origin/feat/remote-only
run "the next call settles: landed on feat/remote-only"                       0 "$(tool PreToolUse $S20 $SL1 Bash t91 '{"command":"git status"}')"
[ ! -e "$FLEET_STATE/leases/repo__${rid3}__feat__p.json" ] && [ -e "$FLEET_STATE/leases/repo__${rid3}__feat__remote-only.json" ] && [ ! -e "$FLEET_STATE/leases/repo__${rid3}__feat__q.json" ] && echo "  ok    landed on feat/remote-only: feat/p dropped, feat/q (origin) released, the destination kept" || { echo "  FAIL  compound settle: $(ls "$FLEET_STATE/leases")"; fails=$((fails+1)); }
git -C "$work/pools/repo3-finisher-1" checkout -q feat/q   # back where the later scenarios expect the tree
# The occupancy lease never overwrites a MACHINE lease under the same slot: name, and a seat cannot be taken by hand.
"$PY" - "$FLEET_STATE" "$SL2" "$H" <<'PY' && echo "  ok    a machine lease under a seat's name survives a SessionStart there; the session is told of the collision" || { echo "  FAIL  occupancy overwrote a resource lease"; fails=$((fails+1)); }
import json, os, subprocess, sys
os.environ["FLEET_STATE"] = sys.argv[1]; sys.path.insert(0, os.environ["FLEET_HOOK_DIR"]); import xlib as hook
sl2, hookpy = sys.argv[2], sys.argv[3]
key = "slot:repo3-finisher-2"
hook._unlink(hook.key_file("leases", key))
hook.write_json(hook.path("sessions", "machine_holder.json"), {"session": "machine_holder", "pid": 999999, "pid_kind": "harness", "last_event_at": 0})
hook.take_lease(key, None, "machine_holder", "r", "/x", "a machine, taken by hand")   # dead holder, NOT an occupancy record
ev = {"hook_event_name": "SessionStart", "session_id": "seat_taker", "cwd": sl2, "source": "startup"}
r = subprocess.run([sys.executable, hookpy], input=json.dumps(ev), capture_output=True, text=True)
after = hook.lease(key)
ok = after and after.get("session") == "machine_holder" and not after.get("occupancy") and "resource lease" in r.stdout
if not ok:
    print("    lease after:", after, "| out:", r.stdout[:200])
hook._unlink(hook.key_file("leases", key)); hook._unlink(hook.path("sessions", "machine_holder.json"))
subprocess.run([sys.executable, hookpy], input=json.dumps({"hook_event_name": "SessionEnd", "session_id": "seat_taker", "cwd": sl2, "reason": "exit"}), capture_output=True, text=True)
sys.exit(0 if ok else 1)
PY
out=$( (cd "$work/pools/repo3" && "$PY" "$F" take slot:repo3-finisher-1 "mine") 2>&1 ); rc=$?
[ "$rc" != 0 ] && case "$out" in *"seat"*) echo "  ok    fleet take refuses a pooled slot's name: a seat is occupied by a session, never taken by hand";; *) echo "  FAIL  take-on-seat text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  a seat was taken as a machine"; fails=$((fails+1)); }
# fleet pool: a plain directory at a slot's path is not a slot; a slot name may not point at two paths.
mkdir -p "$work/pools/repo3-finisher-3"
out=$(ORG_TENANT=work "$PY" "$F" pool "$REPO3" finisher 3 2>&1); rc=$?
[ "$rc" != 0 ] && ! grep -q "repo3-finisher-3" "$ORG_STATE/roles.map" && case "$out" in *"not a worktree"*) echo "  ok    fleet pool refuses a pre-existing directory that git does not know as a worktree";; *) echo "  FAIL  plain-dir text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  pool roled a plain directory (rc=$rc)"; fails=$((fails+1)); }
rmdir "$work/pools/repo3-finisher-3"
mkdir -p "$work/pools2" && git init -q "$work/pools2/repo3" && git -C "$work/pools2/repo3" -c user.email=t@t -c user.name=t commit -q --allow-empty -m init
out=$(ORG_TENANT=work "$PY" "$F" pool "$(native "$work/pools2/repo3")" finisher 1 2>&1); rc=$?
[ "$rc" != 0 ] && [ ! -d "$work/pools2/repo3-finisher-1" ] && case "$out" in *"already names"*) echo "  ok    fleet pool refuses a second checkout with the same basename: one slot name, one worktree, one seat key";; *) echo "  FAIL  same-basename text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  two repos named repo3 got one seat key (rc=$rc)"; fails=$((fails+1)); }
# fleet assign: a revision or a path is not a branch (a detached slot has no branch key to check writes against).
run "slot 2's session ends (the slot must be free for the next refusal to be about the argument)" 0 "$(ev hook_event_name=SessionEnd session_id=$S21 cwd=$SL2 reason=exit)"
sha3=$(git -C "$work/pools/repo3" rev-parse HEAD)
out=$("$PY" "$F" assign repo3-finisher-2 "$sha3" 2>&1); rc=$?
[ "$rc" != 0 ] && [ "$(git -C "$work/pools/repo3-finisher-2" rev-parse --abbrev-ref HEAD)" != "HEAD" ] && case "$out" in *"not a branch name"*|*"not "*) echo "  ok    fleet assign refuses a sha as not a branch: the slot is not left detached";; *) echo "  FAIL  sha refused for the wrong reason: $out"; fails=$((fails+1));; esac || { echo "  FAIL  assign accepted a sha (rc=$rc): $out"; fails=$((fails+1)); }
# Board: a live occupant is not hidden behind a newer dead record at the same path.
"$PY" - "$FLEET_STATE" "$SL1" <<'PY' && echo "  ok    fleet board prefers the live session at a path over a newer dead record there" || { echo "  FAIL  board picked the dead newer record"; fails=$((fails+1)); }
import os, sys, time
os.environ["FLEET_STATE"] = sys.argv[1]; sys.path.insert(0, os.environ["FLEET_HOOK_DIR"]); import xlib as hook; fleet = hook
sl1 = sys.argv[2]
hook.write_json(hook.path("sessions", "bd_newdead.json"), {"session": "bd_newdead", "cwd": sl1, "pid": 999999, "pid_kind": "harness", "last_event_at": hook.now() + 5, "role": "r"})
row = {r["slot"]: r for r in fleet.board_rows()}["repo3-finisher-1"]
ok = row["session"] != "bd_newdead" and row["state"] != "dead"
if not ok:
    print("    row:", row["state"], row["session"])
hook._unlink(hook.path("sessions", "bd_newdead.json"))
sys.exit(0 if ok else 1)
PY
# Receipts: a record without a verdict is malformed, and `done` still answers by exit code.
printf '{"sha":"0000000abc","head":"0000000abcdef","kind":"live","observable":"x","at":1}\n' > "$FLEET_STATE/receipts/0000000abc.live.json"
out=$( (cd "$work/wt2" && "$PY" "$F" done 0000000abc) 2>&1 ); rc=$?
[ "$rc" = 1 ] && case "$out" in *"NOT DONE"*) echo "  ok    a receipt with no verdict is malformed, never a pass; done exits 1";; *) echo "  FAIL  corrupt receipt text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  corrupt receipt rc=$rc: $out"; fails=$((fails+1)); }
rm -f "$FLEET_STATE/receipts/0000000abc.live.json"
# Change lookup never crosses repos while standing in one: #55 is cached for repo3 only.
out=$( (cd "$work/repo2" && "$PY" "$F" who 55) 2>&1 ); rc=$?
[ "$rc" = 1 ] && case "$out" in *"not in this machine's cache"*|*"cache"*) echo "  ok    fleet who #55 inside another repo does not borrow repo3's cached change";; *) echo "  FAIL  cross-repo who text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  who #55 resolved across repos (rc=$rc): $out"; fails=$((fails+1)); }
# The change cache reads the branch from the TREE, never from output a command can print anything into.
run "PostToolUse of 'gh pr create' whose output smuggles a headRefName"      0 "$(ghresp $S20 $SL1 t92 'gh pr create --fill' '{"headRefName":"feat/victim"} https://github.com/acme/repo3/pull/56')"
"$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if r.get('branch')=='feat/q' else 1)" "$FLEET_STATE/prs/${rid3}__56.json" 2>/dev/null && echo "  ok    #56 is bound to the tree's branch (feat/q), not to the headRefName printed in the output" || { echo "  FAIL  output text chose the branch: $(cat "$FLEET_STATE/prs/${rid3}__56.json" 2>/dev/null)"; fails=$((fails+1)); }
run "PostToolUse of 'gh pr checkout 57' (no URL in output; the number is in the command)" 0 "$(ghresp $S20 $SL1 t93 'gh pr checkout 57' 'Switched to branch feat/r')"
[ -e "$FLEET_STATE/prs/${rid3}__57.json" ] && echo "  ok    'gh pr checkout <n>' records the change from the command's own number" || { echo "  FAIL  checkout <n> not cached"; fails=$((fails+1)); }
run "PostToolUse of 'gh pr view --repo acme/repo3 77' (explicit other change)" 0 "$(ghresp $S20 $SL1 t94 'gh pr view --repo acme/repo3 77' 'https://github.com/acme/repo3/pull/77')"
[ ! -e "$FLEET_STATE/prs/${rid3}__77.json" ] && echo "  ok    a flagged 'view <n>' still records nothing about this tree" || { echo "  FAIL  view --repo <n> guessed a branch"; fails=$((fails+1)); }
# MCP: a JSON line that is not an object is answered with -32600 and the server keeps serving.
printf '%s\n' '[{"jsonrpc":"2.0","id":1,"method":"ping"}]' 'null' '{"jsonrpc":"2.0","id":2,"method":"ping"}' | "$PY" "$here/fleet-mcp.py" > "$work/mcp2.out" 2>/dev/null
"$PY" -c "
import json,sys; ls=[json.loads(l) for l in open(sys.argv[1])]
sys.exit(0 if len(ls)==3 and ls[0]['error']['code']==-32600 and ls[1]['error']['code']==-32600 and ls[2].get('result')=={} else 1)" "$work/mcp2.out" && echo "  ok    fleet-mcp survives a batch and a bare null: -32600 each, then answers the next request" || { echo "  FAIL  mcp died on a non-object line: $(cat "$work/mcp2.out")"; fails=$((fails+1)); }
# roles.map is written whole, atomically: a reader mid-write never sees a truncated map.
"$PY" - "$ORG_STATE" "$F" "$REPO3" <<'PY' && echo "  ok    fleet role publishes roles.map by temp-then-replace; a concurrent reader never sees it empty" || { echo "  FAIL  roles.map was observed truncated during a write"; fails=$((fails+1)); }
import os, subprocess, sys, threading, time
org, fleetpy, repo3 = sys.argv[1:4]
mapfile = os.path.join(org, "roles.map")
seen_empty = []
stop = False
def watch():
    while not stop:
        try:
            if os.path.getsize(mapfile) == 0:
                seen_empty.append(time.time())
        except OSError:
            seen_empty.append(("missing", time.time()))
t = threading.Thread(target=watch); t.start()
for _ in range(6):
    subprocess.run([sys.executable, fleetpy, "role", repo3, "finisher:repo3"], capture_output=True, env={**os.environ, "ORG_TENANT": "work"})
stop = True; t.join()
if seen_empty:
    print("    observed empty/missing map", len(seen_empty), "times")
sys.exit(1 if seen_empty else 0)
PY
# ---------- Second panel round: presence, seats, done semantics, unowned, the MCP tools nobody called ----------
# Presence is prefix, role is exact: a tab opened in <slot>/src is IN the worktree for every view that decides whether to touch it.
mkdir -p "$work/pools/repo3-finisher-2/src"
S24=local_2424eeee
run "a session starts in slot 2's src/ subdirectory (no role there)"          0 "$(ev hook_event_name=SessionStart session_id=$S24 cwd=$SL2/src source=startup)"
out=$("$PY" "$F" slots repo3); case "$out" in *"repo3-finisher-2 "*"busy(local_24"*) echo "  ok    fleet slots: a session below the slot's path occupies it";; *) echo "  FAIL  subdirectory session invisible to slots: $out"; fails=$((fails+1));; esac
out=$("$PY" "$F" board); case "$out" in *"repo3-finisher-2 "*"local_24"*) echo "  ok    fleet board sees the same session";; *) echo "  FAIL  board vacant with a session below the path: $out"; fails=$((fails+1));; esac
out=$("$PY" "$F" assign repo3-finisher-2 feat/p 2>&1); rc=$?
[ "$rc" != 0 ] && case "$out" in *busy*) echo "  ok    fleet assign refuses to move a tree a session is sitting in one directory down";; *) echo "  FAIL  assign under a subdirectory session: $out"; fails=$((fails+1));; esac || { echo "  FAIL  assign moved a worktree with a live session below its path"; fails=$((fails+1)); }
out=$(ORG_TENANT=work "$PY" "$F" pool "$REPO3" finisher 2 --rewarm 2>&1)
[ "$(wc -l < "$work/warmcount" | tr -d ' ')" = 3 ] && echo "  ok    --rewarm leaves the slot with a session below its path alone" || { echo "  FAIL  rewarm ran in a slot occupied from a subdirectory ($(wc -l < "$work/warmcount"))"; fails=$((fails+1)); }
run "the subdirectory session ends"                                          0 "$(ev hook_event_name=SessionEnd session_id=$S24 cwd=$SL2/src reason=exit)"
rmdir "$work/pools/repo3-finisher-2/src"
# A seat stays a seat through a revoke; a seat cannot be dropped by hand or handed to a session elsewhere.
S25=local_2525ffff
run "a second session opens in slot 1 (told it is occupied)"                  0 "$(ev hook_event_name=SessionStart session_id=$S25 cwd=$SL1 source=startup)"
out=$( (cd "$work/pools/repo3" && "$PY" "$F" revoke slot:repo3-finisher-1 --to local_2525 "moving the seat") 2>&1 ); rc=$?
[ "$rc" = 0 ] && "$PY" -c "import json,sys; r=json.load(open(sys.argv[1])); sys.exit(0 if r['session']==sys.argv[2] and r.get('occupancy') is True else 1)" "$FLEET_STATE/leases/slot__repo3-finisher-1.json" "$S25" && echo "  ok    fleet revoke on a seat hands it to a session IN that slot and keeps occupancy: true" || { echo "  FAIL  revoke on a seat: rc=$rc $out $(cat "$FLEET_STATE/leases/slot__repo3-finisher-1.json")"; fails=$((fails+1)); }
out=$( (cd "$work/pools/repo3" && "$PY" "$F" revoke slot:repo3-finisher-1 --to local_cccc "to a session elsewhere") 2>&1 ); rc=$?
[ "$rc" != 0 ] && case "$out" in *"seat"*) echo "  ok    fleet revoke refuses to hand a seat to a session whose cwd is not that slot";; *) echo "  FAIL  seat revoke elsewhere text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  a seat was handed to a session in another directory"; fails=$((fails+1)); }
out=$( (cd "$work/pools/repo3-finisher-1" && "$PY" "$F" drop slot:repo3-finisher-1 --session local_2525) 2>&1 ); rc=$?
[ "$rc" != 0 ] && [ -e "$FLEET_STATE/leases/slot__repo3-finisher-1.json" ] && case "$out" in *"seat"*) echo "  ok    fleet drop refuses a seat: it is released when the tab closes, never by hand";; *) echo "  FAIL  drop-seat text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  a seat was dropped by hand (rc=$rc)"; fails=$((fails+1)); }
run "the revoked-to occupant ends"                                            0 "$(ev hook_event_name=SessionEnd session_id=$S25 cwd=$SL1 reason=exit)"
[ ! -e "$FLEET_STATE/leases/slot__repo3-finisher-1.json" ] && echo "  ok    the seat is released when the revoked-to occupant ends (occupancy survived the revoke)" || { echo "  FAIL  a revoked seat outlived its occupant: $(cat "$FLEET_STATE/leases/slot__repo3-finisher-1.json")"; fails=$((fails+1)); }
run "the original occupant resumes and retakes the free seat"                 0 "$(ev hook_event_name=SessionStart session_id=$S20 cwd=$SL1 source=resume)"
# who: a seat's key held as a machine (no occupancy flag) is not an occupant.
"$PY" - "$FLEET_STATE" "$F" <<'PY' && echo "  ok    fleet who on a seat whose key is held as a machine says so, and names nobody" || { echo "  FAIL  who treated a machine lease as a seat"; fails=$((fails+1)); }
import os, subprocess, sys
os.environ["FLEET_STATE"] = sys.argv[1]; sys.path.insert(0, os.environ["FLEET_HOOK_DIR"]); import xlib as hook
key = "slot:repo3-finisher-2"
hook.write_json(hook.path("sessions", "forger.json"), {"session": "forger", "cwd": "/elsewhere", "pid_kind": "parent-unverified", "last_event_at": hook.now(), "role": "r"})
hook.take_lease(key, None, "forger", "r", "/elsewhere", "forged")
r = subprocess.run([sys.executable, sys.argv[2], "who", "repo3-finisher-2"], capture_output=True, text=True)
ok = r.returncode == 1 and "not occupied" in r.stdout and "forger" not in r.stdout.split("->")[-1] if "->" in r.stdout else (r.returncode == 1 and "not occupied" in r.stdout)
if not ok:
    print("    rc:", r.returncode, r.stdout.strip()[:200])
hook._unlink(hook.key_file("leases", key)); hook._unlink(hook.path("sessions", "forger.json"))
sys.exit(0 if ok else 1)
PY
# Change cache: output that links several changes names no single change.
run "PostToolUse of 'gh pr view' whose body links another change first"      0 "$(ghresp $S20 $SL1 t96 'gh pr view' 'Supersedes https://github.com/acme/repo3/pull/12 ... View this pull request on GitHub: https://github.com/acme/repo3/pull/59')"
[ ! -e "$FLEET_STATE/prs/${rid3}__12.json" ] && [ ! -e "$FLEET_STATE/prs/${rid3}__59.json" ] && echo "  ok    two distinct URLs and no explicit target: nothing is recorded (no first-URL guess)" || { echo "  FAIL  guessed from the first URL: $(ls "$FLEET_STATE/prs")"; fails=$((fails+1)); }
# done: expected kinds come from the lanes, a failed receipt is exit 3, and a branch's head is origin's.
printf '{"sha":"1111111abc","head":"1111111abcdef0","kind":"docs","verdict":"pass","observable":"spellcheck ran","at":1}\n' > "$FLEET_STATE/receipts/1111111abc.docs.json"
out=$( (cd "$work/wt2" && "$PY" "$F" done 1111111abc) 2>&1 ); rc=$?
[ "$rc" = 1 ] && case "$out" in *"no receipt of kind live"*) echo "  ok    an unrelated passing receipt does not make a revision DONE: the expected kind comes from the lanes";; *) echo "  FAIL  done text with unrelated receipt: $out"; fails=$((fails+1));; esac || { echo "  FAIL  done rc=$rc on an unrelated receipt: $out"; fails=$((fails+1)); }
rm -f "$FLEET_STATE/receipts/1111111abc.docs.json"
out=$( (cd "$work/wt2" && "$PY" "$F" done "$SHA2" --json) 2>&1 ); rc=$?
[ "$rc" = 0 ] && printf '%s' "$out" | "$PY" -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if d["ok"] is True and d["wanted"]==["live"] and not d["failed"] else 1)' && echo "  ok    fleet done --json: ok, wanted, failed, missing are machine-readable" || { echo "  FAIL  done --json: rc=$rc $out"; fails=$((fails+1)); }
git -C "$work/pools/repo3" -c user.email=t@t -c user.name=t commit -q --allow-empty -m "origin moved" && mkdir -p "$work/pools/repo3/.git/refs/remotes/origin/feat" && git -C "$work/pools/repo3" rev-parse HEAD > "$work/pools/repo3/.git/refs/remotes/origin/feat/q"
orig_q=$(git -C "$work/pools/repo3" rev-parse HEAD); local_q=$(git -C "$work/pools/repo3" rev-parse feat/q)
out=$( (cd "$work/pools/repo3" && "$PY" "$F" done feat/q --json) 2>&1 )
printf '%s' "$out" | "$PY" -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if d["sha"]==sys.argv[1] and d["sha"]!=sys.argv[2] and "origin/" in d["resolution"] and "differs" in d["resolution"] else 1)' "$orig_q" "$local_q" && echo "  ok    fleet done <branch> resolves origin/<branch>, not a stale local ref, and says the two differ" || { echo "  FAIL  branch head resolution: $out"; fails=$((fails+1)); }
rm -f "$work/pools/repo3/.git/refs/remotes/origin/feat/q"
# unowned, with gh unavailable: cached entries, honest scope, working vs unowned from leases in ANY local checkout.
mkdir -p "$work/nogh" && printf '#!/usr/bin/env bash\necho "gh: no auth" >&2; exit 1\n' > "$work/nogh/gh" && chmod +x "$work/nogh/gh"
run "slot 1 writes on feat/q again and holds its lease"                       0 "$(tool PreToolUse $S20 $SL1 Edit t99 "{\"file_path\":\"$SL1/a.ts\"}")"
run "PostToolUse of 'gh pr create' in slot 2 (feat/p, which nobody holds)"    0 "$(ghresp $S21 $SL2 t97 'gh pr create --fill' 'https://github.com/acme/repo3/pull/58')"
out=$( (cd "$work/pools/repo3" && PATH="$work/nogh:$PATH" "$PY" "$F" unowned) 2>&1 ); rc=$?
case "$out" in *"on this machine"*"gh could not list"*"UNOWNED  #58"*"working  #55"*) echo "  ok    fleet unowned: scoped to this machine, gh failure stated, #58 unowned and #55 working from leases";; *"UNOWNED  #58"*"working  #55"*) echo "  ok    fleet unowned classifies from leases (scope/gh lines present: $(printf '%s' "$out" | head -2 | tr '\n' ' '))";; *) echo "  FAIL  unowned: rc=$rc $out"; fails=$((fails+1));; esac
out=$( (cd "$work/pools/repo3" && PATH="$work/nogh:$PATH" "$PY" "$F" unowned --json) 2>&1 )
printf '%s' "$out" | "$PY" -c 'import json,sys; d=json.load(sys.stdin); u={r["number"] for r in d["unowned"]}; w={r["number"] for r in d["working"]}; sys.exit(0 if 58 in u and 55 in w and 55 not in u and "this machine" in d["scope"] else 1)' && echo "  ok    fleet unowned --json carries scope, unowned and working" || { echo "  FAIL  unowned --json: $out"; fails=$((fails+1)); }
# slots: cold (warm failed or config changed) and missing (directory gone); assign refuses dirty.
"$PY" -c "import json,sys; p=sys.argv[1]; c=json.load(open(p)); c['repo3']['warm']='exit 3'; c['repo3']['slots']['author']=1; json.dump(c,open(p,'w'))" "$FLEET_STATE/pools.json"
out=$(ORG_TENANT=work "$PY" "$F" pool "$REPO3" author 1 2>&1); rc=$?
[ "$rc" = 0 ] && case "$out" in *"repo3-author-1 (FAILED exit 3"*) echo "  ok    fleet pool reports a failed warm by exit code";; *) echo "  FAIL  warm failure not reported: $out"; fails=$((fails+1));; esac || { echo "  FAIL  pool with a failing warm: rc=$rc $out"; fails=$((fails+1)); }
out=$("$PY" "$F" slots repo3); case "$out" in *"repo3-author-1 "*free*cold*) echo "  ok    fleet slots marks a slot whose warm failed as cold";; *) echo "  FAIL  cold not shown: $out"; fails=$((fails+1));; esac
case "$out" in *"repo3-finisher-2 "*cold*) echo "  ok    a warm older than pools.json is stale: the slot is cold again after the config changed";; *) echo "  FAIL  stale warm not detected after pools.json changed: $out"; fails=$((fails+1));; esac
printf 'dirt\n' > "$work/pools/repo3-author-1/dirt.txt"
out=$("$PY" "$F" assign repo3-author-1 feat/p 2>&1); rc=$?
[ "$rc" != 0 ] && case "$out" in *dirty*) echo "  ok    fleet assign refuses a dirty slot";; *) echo "  FAIL  dirty refusal text: $out"; fails=$((fails+1));; esac || { echo "  FAIL  assign into a dirty slot accepted"; fails=$((fails+1)); }
rm -rf "$work/pools/repo3-author-1"
out=$("$PY" "$F" slots repo3); case "$out" in *"repo3-author-1 "*missing*) echo "  ok    fleet slots reports a slot whose directory is gone as missing";; *) echo "  FAIL  missing not shown: $out"; fails=$((fails+1));; esac
out=$("$PY" "$F" slots repo3 --json); printf '%s' "$out" | "$PY" -c 'import json,sys; rows={r["slot"]: r for r in json.load(sys.stdin)}; sys.exit(0 if rows["repo3-author-1"]["state"]=="missing" and rows["repo3-finisher-1"]["state"]=="busy" and "path" in rows["repo3-finisher-1"] else 1)' && echo "  ok    fleet slots --json is machine-readable" || { echo "  FAIL  slots --json: $out"; fails=$((fails+1)); }
out=$("$PY" "$F" board --json); printf '%s' "$out" | "$PY" -c 'import json,sys; rows=json.load(sys.stdin); r=[x for x in rows if x["slot"]=="repo3-finisher-1"][0]; sys.exit(0 if r["state"] in ("idle","busy","idle-holding-work") and r["session"]==sys.argv[1] and "cadence" in r else 1)' "$S20" && echo "  ok    fleet board --json is machine-readable" || { echo "  FAIL  board --json: $out"; fails=$((fails+1)); }
out=$("$PY" "$F" who repo3-finisher-1 --json); printf '%s' "$out" | "$PY" -c 'import json,sys; d=json.load(sys.stdin); sys.exit(0 if d["resolved"] is True and d["lease"]["session"]==sys.argv[1] else 1)' "$S20" && echo "  ok    fleet who --json is machine-readable" || { echo "  FAIL  who --json: $out"; fails=$((fails+1)); }
git -C "$work/pools/repo3" worktree prune
# A tool call after Stop with no prompt reopens the turn NOW, not against the previous turn's start.
run "slot 1's session stops its turn"                                        0 "$(ev hook_event_name=Stop session_id=$S20 cwd=$SL1)"
age "$FLEET_STATE/sessions/$S20.json" turn_open_at 7200
run "a tool call arrives with no prompt in between"                          0 "$(tool PreToolUse $S20 $SL1 Bash t98 '{"command":"git status"}')"
"$PY" -c "import json,sys,time; r=json.load(open(sys.argv[1])); sys.exit(0 if r.get('turn_open') and time.time()-r.get('turn_open_at',0) < 120 else 1)" "$FLEET_STATE/sessions/$S20.json" && echo "  ok    turn_open_at is refreshed when a tool call reopens a closed turn (no false busy-and-overdue)" || { echo "  FAIL  stale turn_open_at: $(cat "$FLEET_STATE/sessions/$S20.json")"; fails=$((fails+1)); }
# MCP: the five tools the first scenario never called — slots, board, unowned, take, drop — each over the same functions.
run "the liverun session is live for the MCP acts"                           0 "$(ev hook_event_name=SessionStart session_id=$S11 cwd=$WT2 source=resume)"
printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}' \
  "{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"fleet_slots\",\"arguments\":{\"repo\":\"repo3\",\"cwd\":\"$REPO3\"}}}" \
  "{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"fleet_board\",\"arguments\":{\"cwd\":\"$REPO3\"}}}" \
  "{\"jsonrpc\":\"2.0\",\"id\":4,\"method\":\"tools/call\",\"params\":{\"name\":\"fleet_unowned\",\"arguments\":{\"cwd\":\"$REPO3\"}}}" \
  "{\"jsonrpc\":\"2.0\",\"id\":5,\"method\":\"tools/call\",\"params\":{\"name\":\"fleet_take\",\"arguments\":{\"resource\":\"slot:hyper\",\"why\":\"mcp run\",\"cwd\":\"$WT2\"}}}" \
  "{\"jsonrpc\":\"2.0\",\"id\":6,\"method\":\"tools/call\",\"params\":{\"name\":\"fleet_who\",\"arguments\":{\"name\":\"slot:hyper\",\"cwd\":\"$WT2\"}}}" \
  "{\"jsonrpc\":\"2.0\",\"id\":7,\"method\":\"tools/call\",\"params\":{\"name\":\"fleet_drop\",\"arguments\":{\"resource\":\"slot:hyper\",\"cwd\":\"$WT2\"}}}" \
  "{\"jsonrpc\":\"2.0\",\"id\":8,\"method\":\"tools/call\",\"params\":{\"name\":\"fleet_take\",\"arguments\":{\"resource\":\"slot:repo3-finisher-1\",\"cwd\":\"$WT2\"}}}" \
  | (cd "$work" && PATH="$work/nogh:$PATH" "$PY" "$here/fleet-mcp.py") > "$work/mcp3.out" 2>"$work/mcp3.err"
"$PY" - "$work/mcp3.out" "$S11" "$S20" <<'PY' && echo "  ok    fleet-mcp: slots, board and unowned answer as the CLI does; take then drop act for the session at cwd; a seat cannot be taken" || { echo "  FAIL  fleet-mcp tools: $(cat "$work/mcp3.out" "$work/mcp3.err" | head -c 1500)"; fails=$((fails+1)); }
import json, sys
by = {}
for line in open(sys.argv[1], encoding="utf-8"):
    d = json.loads(line); by[d["id"]] = d
s11, s20 = sys.argv[2], sys.argv[3]
text = lambda i: by[i]["result"]["content"][0]["text"]
bad = []
slots = json.loads(text(2))
if not any(r["slot"] == "repo3-finisher-1" and r["state"] == "busy" and r["session"] == s20 for r in slots): bad.append("slots")
board = json.loads(text(3))
if not any(r["slot"] == "repo3-finisher-1" and r["session"] == s20 for r in board): bad.append("board")
un = json.loads(text(4))
if "this machine" not in un["scope"] or not any(r["number"] == 58 for r in un["unowned"]): bad.append("unowned")
if by[5]["result"]["isError"] or "taken by" not in text(5) or s11[:8] not in text(5): bad.append("take: %s" % text(5)[:120])
if by[6]["result"]["isError"] or s11 not in text(6): bad.append("who after take")
if by[7]["result"]["isError"] or "dropped by" not in text(7): bad.append("drop: %s" % text(7)[:120])
if not by[8]["result"]["isError"] or "seat" not in text(8): bad.append("take on a seat via MCP: %s" % text(8)[:120])
for b in bad: print("   ", b)
sys.exit(1 if bad else 0)
PY
[ ! -e "$FLEET_STATE/leases/slot__hyper.json" ] && echo "  ok    the MCP drop released the resource" || { echo "  FAIL  resource still held after MCP drop"; fails=$((fails+1)); }
run "the liverun session ends again"                                        0 "$(ev hook_event_name=SessionEnd session_id=$S11 cwd=$WT2 reason=exit)"
run "slot 1's session ends"                                                  0 "$(ev hook_event_name=SessionEnd session_id=$S20 cwd=$SL1 reason=exit)"
run "slot 2's session ends"                                                  0 "$(ev hook_event_name=SessionEnd session_id=$S21 cwd=$SL2 reason=exit)"
"$PY" -c "import os,sys; os.environ['FLEET_STATE']=sys.argv[1]; sys.path.insert(0, os.environ['FLEET_HOOK_DIR']); import xlib as hook; hook.drop_lease(hook.scope(sys.argv[2], 'feat/r'), 'local_2222cccc'); hook._unlink(hook.path('sessions','local_2222cccc.json'))" "$FLEET_STATE" "$REPO3"

# Defect 1: two role templates shipped deny rules the harness silently drops. `:*` is the
# prefix-match terminator, so it is only meaningful at the very end; `Bash(gh api:*requested_reviewers*)`
# is rejected and skipped, and the role runs with a deny rule it believes it has. A permission that
# fails open without saying so is the same failure as a hook that will not spawn, so it gets a check.
"$PY" - "$LANES" <<'PY' && echo "  ok    every lane deny rule is a shape the harness accepts" || { echo "  FAIL  a lane manifest carries a deny rule the harness will skip"; fails=$((fails+1)); }
import glob, json, os, re, sys
bad = []
for f in sorted(glob.glob(os.path.join(sys.argv[1], "*", "manifest.json"))):
    for rule in json.load(open(f, encoding="utf-8"))["denies"]:
        m = re.fullmatch(r"[A-Za-z]+\((.*)\)", rule)
        if not m:
            bad.append((os.path.basename(f), rule, "not Tool(pattern)"))
        elif ":*" in m.group(1) and not m.group(1).endswith(":*"):
            bad.append((os.path.basename(f), rule, "`:*` mid-pattern is rejected; use a space"))
for f, rule, why in bad:
    print("    %s: %s -- %s" % (f, rule, why))
sys.exit(1 if bad else 0)
PY

# Defect 14: an unencodable character in a verb's own output killed the verb. On Windows stdout is
# cp1252 with errors=strict, so one U+2192 raised mid-print. Runs last: it appends a decision row,
# and the earlier decision scenarios assert on ids.
"$PY" "$F" decide rule glyphs "arrow → dash — middot · must not abort the verb" >"$work/glyph.out" 2>"$work/glyph.err"
if [ $? = 0 ] && ! grep -q UnicodeEncodeError "$work/glyph.err" && grep -q glyphs "$work/glyph.out"; then
  echo "  ok    a verb whose output carries non-cp1252 glyphs completes instead of raising"
else
  echo "  FAIL  verb aborted on its own output: $(cat "$work/glyph.err")"; fails=$((fails+1))
fi
# ---- Go port, rung 2: the session record under its own lock (ruling 8); role bound at the launch
# directory (a cd is not a change of who you are); the last word observed at Stop (ruling 6). Each is
# discriminating: revert the change and its line goes red.
"$PY" - "$FLEET_STATE" "$H" "$WT" "$work" <<'PY' || fails=$((fails+1))
import json, os, subprocess, sys, time
state, hookpy, wt, work = sys.argv[1:5]
os.environ["FLEET_STATE"] = state; sys.path.insert(0, os.environ["FLEET_HOOK_DIR"]); import xlib as hook
def hookrun(ev, env=None):
    return subprocess.run([sys.executable, hookpy], input=json.dumps(ev), capture_output=True, text=True, env={**os.environ, **(env or {})})
bad = 0
def report(ok, good, badtext):
    global bad
    print("  ok    " + good if ok else "  FAIL  " + badtext)
    bad += 0 if ok else 1
# 2a: two hooks for one session at once. A is paused between its read and its write of the session
# record; B records a branch-switch handoff meanwhile. Unlocked, A's stale write dropped B's handoff.
r = os.path.join(work, "rung2repo")
subprocess.run(["git", "init", "-q", r]); subprocess.run(["git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "i"], cwd=r)
subprocess.run(["git", "branch", "feat/q"], cwd=r); subprocess.run(["git", "checkout", "-q", "-b", "feat/a"], cwd=r)
sid = "rung2_sess"
hookrun({"hook_event_name": "SessionStart", "session_id": sid, "cwd": r, "source": "startup"})
a = subprocess.Popen([sys.executable, hookpy], stdin=subprocess.PIPE, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, text=True, env={**os.environ, "FLEET_TEST_PAUSE_IN_TOUCH": "0.6"})
a.stdin.write(json.dumps({"hook_event_name": "PreToolUse", "session_id": sid, "cwd": r, "tool_name": "Bash", "tool_use_id": "r2a", "tool_input": {"command": "git status"}})); a.stdin.close()
time.sleep(0.2)
hookrun({"hook_event_name": "PreToolUse", "session_id": sid, "cwd": r, "tool_name": "Bash", "tool_use_id": "r2b", "tool_input": {"command": "git checkout feat/q"}})
a.wait()
rec = hook.read_json(hook.path("sessions", f"{sid}.json")) or {}
report(isinstance(rec.get("handoff"), dict), "the session record is written under its own lock: a parallel touch cannot drop a handoff in flight", "a parallel touch dropped the handoff in flight (session record unlocked): " + json.dumps(rec)[:200])
# 2b: identity is the launch directory. Started unroled, one event from inside a roled checkout.
sid2 = "rung2_unroled"
hookrun({"hook_event_name": "SessionStart", "session_id": sid2, "cwd": work, "source": "startup"})
hookrun({"hook_event_name": "PreToolUse", "session_id": sid2, "cwd": wt, "tool_name": "Bash", "tool_use_id": "r2c", "tool_input": {"command": "git status"}})
rec2 = hook.read_json(hook.path("sessions", f"{sid2}.json")) or {}
report(not rec2.get("role") and rec2.get("launch_dir") == work, "role binds at the launch directory: a cd into a roled checkout does not re-role the session", f"a cd into a roled checkout re-roled the session: role={rec2.get('role')!r} launch_dir={rec2.get('launch_dir')!r}")
# 2c: the last word. Stop carries the transcript path; the final assistant message is recorded
# against the branch and injected at the next SessionStart there, with no act by the agent.
tp = os.path.join(work, "rung2-transcript.jsonl")
open(tp, "w").write(json.dumps({"type": "user", "message": {"content": "do the thing"}}) + "\n"
                    + json.dumps({"type": "assistant", "message": {"role": "assistant", "content": [{"type": "text", "text": "Fixed the parser; the guard now fires on the reverted bug.   Next: wire the receipt."}]}}) + "\n")
hookrun({"hook_event_name": "Stop", "session_id": sid, "cwd": r, "transcript_path": tp})
key = hook.scope(r, "feat/a")
lw = hook.read_json(hook.key_file("last-word", key)) or {}
out = hookrun({"hook_event_name": "SessionStart", "session_id": "rung2_successor", "cwd": r, "source": "startup"}).stdout
report("guard now fires" in (lw.get("text") or "") and "last word on feat/a" in out and "guard now fires" in out, "the last word is observed at Stop from the transcript and injected at the next SessionStart on that branch", "last word not recorded or not injected: " + json.dumps(lw)[:120] + " | " + out[:160].replace("\n", " "))
for s in (sid, sid2, "rung2_successor"):
    hookrun({"hook_event_name": "SessionEnd", "session_id": s, "cwd": r, "reason": "exit"})
sys.exit(1 if bad else 0)
PY

# ---- Go port, rung 3: fleet watch, the read-only watcher. One tick folds the store into board.json
# and an attention-budgeted board.md, records each transition, and ticks a clock nobody else has; a
# gap longer than three intervals is a slept machine, and silence inside it is `unknown`, not `dead`.
"$PY" - "$FLEET_STATE" "$F" "$work" <<'PY' || fails=$((fails+1))
import json, os, subprocess, sys, time
state, fleetpy, work = sys.argv[1:4]
os.environ["FLEET_STATE"] = state; sys.path.insert(0, os.environ["FLEET_HOOK_DIR"]); import xlib as hook
bad = 0
def report(ok, good, badtext):
    global bad
    print("  ok    " + good if ok else "  FAIL  " + badtext); bad += 0 if ok else 1
def fleet(*a): return subprocess.run([sys.executable, fleetpy, *a], capture_output=True, text=True, env={**os.environ, "ORG_TENANT": "work"})
# A roled path of this scenario's own, with a dead session holding a branch lease there.
r = os.path.join(work, "watchrepo"); subprocess.run(["git", "init", "-q", r])
subprocess.run(["git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "i"], cwd=r); subprocess.run(["git", "checkout", "-q", "-b", "feat/w"], cwd=r)
fleet("role", r, "finisher:watchrepo", "--tenant", "work")
hook.write_json(hook.path("sessions", "watch_dead.json"), {"session": "watch_dead", "cwd": r, "pid": 999999, "pid_kind": "harness", "last_event_at": hook.now() - 600, "role": "finisher:watchrepo", "branch": "feat/w", "turn_open": False})
key = hook.scope(r, "feat/w"); hook.acquire_lease(key, hook.lease_record(key, "watch_dead", "finisher:watchrepo", r, "held"))
wd = hook.path("watch")
for f in ("heartbeat.json", "board.json", "board.md", "observed.jsonl"):
    hook._unlink(os.path.join(wd, f))
t1 = fleet("watch", "--once", "--interval", "60s")
rows = {x["path"]: x for x in (hook.read_json(os.path.join(wd, "board.json")) or [])}
row = rows.get(hook.read_json(hook.path("sessions", "watch_dead.json"))["cwd"]) or next((x for x in rows.values() if x.get("role") == "finisher:watchrepo"), {})
report(t1.returncode == 0 and os.path.exists(os.path.join(wd, "heartbeat.json")) and row.get("state") == "dead-holding-work" and "Needs a decision" in t1.stdout and "dead-holding-work" in t1.stdout and "feat/w" in t1.stdout,
       "fleet watch --once writes the board and puts a dead session holding work under 'Needs a decision'",
       f"watch tick: rc={t1.returncode} state={row.get('state')} out={t1.stdout[:220]!r} err={t1.stderr[:120]!r}")
obs = [json.loads(l) for l in open(os.path.join(wd, "observed.jsonl")) if l.strip()] if os.path.exists(os.path.join(wd, "observed.jsonl")) else []
report(any(o.get("to") == "dead-holding-work" and o.get("role") == "finisher:watchrepo" for o in obs) and "fine (" in t1.stdout,
       "the tick records the transition to observed.jsonl and collapses everything fine into one line with a count",
       f"observed={obs[-3:]} fine-line={'fine (' in t1.stdout}")
# The machine sleeps: the heartbeat is four intervals old. The dead classification's only evidence is the
# silence inside that gap, so the row must read unknown — and read dead again once a fresh tick has seen it.
hb = hook.path("watch", "heartbeat.json"); h = hook.read_json(hb); h["at"] = hook.now() - 4 * 60; hook.write_json(hb, h)
sess = hook.path("sessions", "watch_dead.json"); sd = hook.read_json(sess); sd["last_event_at"] = hook.now() - 120; hook.write_json(sess, sd)   # last seen inside the gap
t2 = fleet("watch", "--once", "--interval", "60s")
rows2 = {x.get("role"): x for x in (hook.read_json(os.path.join(wd, "board.json")) or [])}
t3 = fleet("watch", "--once", "--interval", "60s")
rows3 = {x.get("role"): x for x in (hook.read_json(os.path.join(wd, "board.json")) or [])}
report(rows2.get("finisher:watchrepo", {}).get("state") == "unknown" and "slept" in t2.stdout and rows3.get("finisher:watchrepo", {}).get("state") == "dead-holding-work",
       "after a slept gap the silent row reads unknown, not dead, and reads dead again on the next fresh tick",
       f"after sleep: {rows2.get('finisher:watchrepo', {}).get('state')} | after fresh tick: {rows3.get('finisher:watchrepo', {}).get('state')} | out2={t2.stdout[:160]!r}")
hook.drop_lease(key, "watch_dead"); hook._unlink(sess)
sys.exit(1 if bad else 0)
PY

# ---- Go port, rung 4: the board delta at a hub's prompt (manifest `watch: true`, no domain word),
# and `--for` on assign: who is accountable, distinct from who dispatched.
"$PY" - "$FLEET_STATE" "$H" "$F" "$REPO" "$WT" "$work" <<'PY' || fails=$((fails+1))
import json, os, subprocess, sys, time
state, hookpy, fleetpy, repo, wt, work = sys.argv[1:7]
os.environ["FLEET_STATE"] = state; sys.path.insert(0, os.environ["FLEET_HOOK_DIR"]); import xlib as hook
bad = 0
def report(ok, good, badtext):
    global bad
    print("  ok    " + good if ok else "  FAIL  " + badtext); bad += 0 if ok else 1
def hookrun(ev): return subprocess.run([sys.executable, hookpy], input=json.dumps(ev), capture_output=True, text=True)
def ctx(out):
    try: return json.loads(out)["hookSpecificOutput"]["additionalContext"]
    except Exception: return out
def fleet(*a): return subprocess.run([sys.executable, fleetpy, *a], capture_output=True, text=True, env={**os.environ, "ORG_TENANT": "work"})
# 4a: a dead session holding work on the roled watchrepo path; one watcher tick; then a prompt in a lane
# whose manifest says watch:true (the shipped supervisor's) carries the attention row, and a lane
# without it does not. Nothing here names the lane kind: the hook read a boolean off a manifest.
r = os.path.join(work, "watchrepo")
hook.write_json(hook.path("sessions", "watch_dead2.json"), {"session": "watch_dead2", "cwd": r, "pid": 999999, "pid_kind": "harness", "last_event_at": hook.now() - 600, "role": "finisher:watchrepo", "branch": "feat/w", "turn_open": False})
key = hook.scope(r, "feat/w"); hook.acquire_lease(key, hook.lease_record(key, "watch_dead2", "finisher:watchrepo", r, "held"))
fleet("watch", "--once", "--interval", "60s")
hub, worker = "rung4_hub", "rung4_worker"
hookrun({"hook_event_name": "SessionStart", "session_id": hub, "cwd": repo, "source": "startup"})
hookrun({"hook_event_name": "SessionStart", "session_id": worker, "cwd": wt, "source": "startup"})
h1 = ctx(hookrun({"hook_event_name": "UserPromptSubmit", "session_id": hub, "cwd": repo, "prompt": "how is it going"}).stdout)
w1 = ctx(hookrun({"hook_event_name": "UserPromptSubmit", "session_id": worker, "cwd": wt, "prompt": "hi"}).stdout)
report("[fleet] board" in h1 and "dead-holding-work" in h1 and "finisher:watchrepo" in h1 and "[fleet] board" not in w1,
       "a lane with watch:true gets the board's attention rows at its prompt; a lane without it does not",
       f"hub prompt: {h1[:220]!r} | worker prompt: {w1[:120]!r}")
# a change since the hub's last prompt: the dead session is released, the row goes vacant, the next prompt says so
hook.drop_lease(key, "watch_dead2"); hook._unlink(hook.path("sessions", "watch_dead2.json"))
fleet("watch", "--once", "--interval", "60s")
h2 = ctx(hookrun({"hook_event_name": "UserPromptSubmit", "session_id": hub, "cwd": repo, "prompt": "and now"}).stdout)
report("since your last prompt" in h2 and "dead-holding-work → vacant" in h2,
       "the hub's next prompt carries what changed since its last one, from the watcher's transitions",
       f"hub prompt 2: {h2[:260]!r}")
# 4b: assign --for records the accountable role, distinct from by; slots shows it.
subprocess.run(["git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "i"], cwd=r)
subprocess.run(["git", "branch", "feat/w2"], cwd=r, capture_output=True)
fleet("pool", r, "finisher", "1")
a = fleet("assign", "watchrepo-finisher-1", "feat/w2", "finish this", "--for", "hub:alpha")
rec = hook.read_json(hook.path("assign", "watchrepo-finisher-1.json")) or {}
sl = fleet("slots", "watchrepo").stdout
report(a.returncode == 0 and rec.get("for") == "hub:alpha" and rec.get("by") == "operator" and "assigned(feat/w2 for hub:alpha)" in sl,
       "fleet assign --for records the accountable role on the row, distinct from who dispatched, and slots shows it",
       f"assign rc={a.returncode} {(a.stdout+a.stderr)[:160]!r} rec={rec} slots={sl[:160]!r}")
fleet("unassign", "watchrepo-finisher-1")
for sid in (hub, worker):
    hookrun({"hook_event_name": "SessionEnd", "session_id": sid, "cwd": repo, "reason": "exit"})
sys.exit(1 if bad else 0)
PY

echo; "$PY" "$F" sessions; echo; "$PY" "$F" leases; echo; "$PY" "$F" decisions
echo; echo "python invocation for the six matchers: $(hook_invocation)"
echo "liveness path on this box: $(liveness_path)"
rm -rf "$work"
[ "$fails" = 0 ] && { echo; echo "all scenarios passed"; exit 0; }

echo; echo "$fails scenario(s) FAILED"; exit 1
