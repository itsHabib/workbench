package fleet

import (
	"fmt"
	"os"
	"strings"
)

// Verdict is what a hook event produces: an exit code, stderr (a denial's reason)
// and stdout (context for the session). Deny = 2 with the reason on stderr; the
// harness feeds stderr to the model. Context = 0 with a hookSpecificOutput object.
type Verdict struct {
	Code int
	Err  string
	Out  string
}

func deny(reason string) *Verdict { return &Verdict{Code: 2, Err: reason + "\n"} }

func context(ev Event, text string) *Verdict {
	out := DumpJSON(Rec{"hookSpecificOutput": Rec{"hookEventName": S(ev, "hook_event_name"), "additionalContext": text}})
	return &Verdict{Out: string(out) + "\n"}
}

var allow = &Verdict{}

// Run handles one event. It never panics out: the fail-open law says an internal
// error exits 0 with no output, logged — except on the lease path, which CheckLease
// already turns into a refusal before it can reach here.
func Run(ev Event) (v *Verdict) {
	defer func() {
		if r := recover(); r != nil {
			logError(Rec{"error": fmt.Sprint(r)})
			v = allow
		}
	}()
	sid := S(ev, "session_id")
	if sid == "" {
		sid = "unknown"
	}
	MigrateLegacyKeys() // every event, before any key is read; marker-guarded
	switch S(ev, "hook_event_name") {
	case "SessionStart":
		return onSessionStart(ev, sid)
	case "UserPromptSubmit":
		return onPrompt(ev, sid)
	case "PreToolUse":
		return onPreTool(ev, sid)
	case "PostToolUse":
		return onPostTool(ev, sid)
	case "Stop":
		return onStop(ev, sid)
	case "SessionEnd":
		return onSessionEnd(ev, sid)
	}
	return allow
}

func onSessionStart(ev Event, sid string) *Verdict {
	rec := TouchSession(sid, ev, Rec{"turn_open": false, "ended": false})
	role, branch := S(rec, "role"), S(rec, "branch")
	if role == "" {
		role = "?"
	}
	if branch == "" {
		branch = "(detached/none)"
	}
	line := fmt.Sprintf("[fleet] session %s · role %s · branch %s", Short(sid), role, branch)
	if slot := S(rec, "slot"); slot != "" {
		line += " · slot " + slot
	}
	lines := []string{line}
	if slot := S(rec, "slot"); slot != "" {
		if contested := occupySlot(sid, slot, S(rec, "role"), S(rec, "cwd")); contested != "" {
			lines = append(lines, contested)
		}
		if al := AssignmentLine(slot, sid); al != "" {
			lines = append(lines, al)
		}
	}
	cwd := S(rec, "cwd")
	if cwd == "" {
		cwd = "."
	}
	if b := S(rec, "branch"); b != "" {
		key := Scope(cwd, b)
		flag, cur := StopFlag(key), Lease(key)
		// Same exemption as CheckStop: a revoke's flag names the one session allowed
		// to act, and telling that session it is stopped made it stand down on resume.
		if flag != nil && S(flag, "except") != sid {
			lines = append(lines, fmt.Sprintf("[fleet] STOP flag on %s: %s", b, S(flag, "reason")))
		}
		if IsMalformed(cur) {
			lines = append(lines, fmt.Sprintf("[fleet] the lease file for %s is malformed (%s); writes there will be refused until it is removed", b, S(cur, "malformed")))
		} else if cur != nil && S(cur, "session") != sid {
			r := S(cur, "role")
			if r == "" {
				r = "session"
			}
			lines = append(lines, fmt.Sprintf("[fleet] %s is held by %s %s — you cannot write to it", b, r, Short(S(cur, "session"))))
		}
	}
	lane := M(rec, "lane")
	if S(rec, "role") != "" && lane == nil {
		lines = append(lines, fmt.Sprintf("[fleet] role %s has no manifest under lanes/; requires and produces unchecked", strings.SplitN(S(rec, "role"), ":", 2)[0]))
	}
	for _, r := range Strs(lane, "requires") {
		state, cur := HeldByOther(r, sid)
		switch {
		case state == HeldFree && cur != nil:
			lines = append(lines, fmt.Sprintf("[fleet] %s: held by you", r))
		case state == HeldFree:
			lines = append(lines, fmt.Sprintf("[fleet] %s requires %s before any effectful call; it is free — `fleet take %s \"<why>\"`", S(lane, "kind"), r, r))
		case state == HeldOrphaned || state == HeldDead:
			lines = append(lines, fmt.Sprintf("[fleet] %s is orphaned by dead session %s; `fleet take %s --takeover \"<what you checked>\"` once it is quiet", r, Short(S(cur, "session")), r))
		case state == HeldMalformed:
			lines = append(lines, fmt.Sprintf("[fleet] %s: lease file malformed (%s)", r, S(cur, "malformed")))
		default:
			hr := S(cur, "role")
			if hr == "" {
				hr = "session"
			}
			lines = append(lines, fmt.Sprintf("[fleet] %s is held by %s %s", r, hr, Short(S(cur, "session"))))
		}
	}
	if rows := CostRows(); len(rows) > 0 {
		if len(rows) > 8 {
			rows = rows[:8]
		}
		var parts []string
		for _, r := range rows {
			parts = append(parts, fmt.Sprintf("`%s` %ds (n=%d)", r.Sig, int(r.Med), r.N))
		}
		lines = append(lines, "[fleet] measured costs on this machine (median, n): "+strings.Join(parts, "; "))
	}
	if dl := DecisionsLine(); dl != "" {
		lines = append(lines, dl)
	}
	if b := S(rec, "branch"); b != "" {
		if hl := HandoffLine(Scope(cwd, b), b); hl != "" {
			lines = append(lines, hl)
		}
	}
	return context(ev, strings.Join(lines, "\n"))
}

func onPrompt(ev Event, sid string) *Verdict {
	prev := SessionRecord(sid)
	// `turn_open_at` is what the board measures a busy session's overdue-ness from.
	TouchSession(sid, ev, Rec{"turn_open": true, "turn_open_at": Now()})
	var lines []string
	if last := F(prev, "last_stop_at"); last > 0 {
		if gap := Now() - last; gap > 60 {
			lines = append(lines, fmt.Sprintf("[fleet] %s passed since your last turn ended.", FmtAge(gap)))
		}
	}
	lines = append(lines, resumeLines(sid, prev, ev)...)
	if dl := DecisionsLine(); dl != "" {
		lines = append(lines, dl)
	}
	if len(lines) == 0 {
		return allow
	}
	return context(ev, strings.Join(lines, "\n"))
}

// resumeLines tells a session that a stop which refused it has been lifted. A
// stand-down is delivered at the next tool call, so lifting it is delivered the same
// way: at the next turn, positively, once.
func resumeLines(sid string, prev Rec, ev Event) []string {
	ld := M(prev, "last_denied")
	if S(ld, "kind") != "stop" || StopFlag(S(ld, "key")) != nil {
		return nil
	}
	TouchSession(sid, ev, Rec{"last_denied": nil}) // said once; not every turn from here on
	// A revoke's flag retires as soon as it has been delivered, so "no flag" after a
	// revoke does not mean "yours again": the lease moved.
	if B(ld, "revoke") {
		cur := Lease(S(ld, "key"))
		if cur != nil && !IsMalformed(cur) && S(cur, "session") != "" && S(cur, "session") != sid && SessionAlive(ReadJSON(Path("sessions", S(cur, "session")+".json"))) {
			r := S(cur, "role")
			if r == "" {
				r = "session"
			}
			return []string{fmt.Sprintf("[fleet] the stand-down on %s that refused you %s ago was a revoke: the branch is now held by %s %s and you cannot write to it. Work elsewhere; do not retry.",
				S(ld, "branch"), FmtAge(Now()-F(ld, "at")), r, Short(S(cur, "session")))}
		}
	}
	return []string{fmt.Sprintf("[fleet] no stop flag on %s any more — the stand-down that refused you %s ago has been lifted. You may act on that branch again; do not keep standing down from memory of the refusal.",
		S(ld, "branch"), FmtAge(Now()-F(ld, "at")))}
}

func onPreTool(ev Event, sid string) *Verdict {
	// Order matters. TouchSession writes the session record, and every write is
	// fallible; a failure there once reached the fail-open catch and the tool call
	// proceeded with no lease check. So: read the cached record, reach the stop and
	// lease verdicts, and only then write.
	rec := SessionRecord(sid)
	// A branch switch from the previous call is settled first, from HEAD.
	SettleHandoff(sid, rec)
	tool := S(ev, "tool_name")
	inp := M(ev, "tool_input")
	cmd := S(inp, "command")
	// The EVENT's cwd first, exactly as TouchSession would have recorded it.
	target := S(inp, "file_path")
	if target == "" {
		target = S(inp, "notebook_path")
	}
	if target == "" {
		target = S(ev, "cwd")
	}
	if target == "" {
		target = S(rec, "cwd")
	}
	if target == "" {
		target = "."
	}
	if tool == "Bash" {
		target = BashTarget(cmd, target)
	}
	branch := BranchOf(target)
	key := ""
	if branch != "" {
		key = Scope(target, branch)
	}
	evCwd := S(ev, "cwd")
	if evCwd == "" {
		evCwd = S(rec, "cwd")
	}
	if branch != "" {
		if reason := CheckStop(key, branch, sid); reason != "" {
			// Record that a stop refused this session, so the next turn can tell it
			// the flag is gone.
			flag := StopFlag(key)
			TouchSession(sid, ev, Rec{"last_denied": Rec{"kind": "stop", "branch": branch, "key": key, "at": Now(), "revoke": S(flag, "except") != ""}})
			RetireDeliveredStop(key, sid)
			return deny(reason)
		}
	}
	writes := IsWrite(tool, cmd)
	if branch != "" && writes {
		if reason := CheckLease(key, branch, sid, S(rec, "role"), evCwd); reason != "" {
			return deny(reason)
		}
	}
	// A branch switch leases the DESTINATION before git runs: refused if a live
	// session holds it, and nothing has moved. The origin stays held until the next
	// hook reads HEAD and sees where the tree landed.
	var toKeys []string
	if tool == "Bash" {
		for _, to := range uniq(SwitchTargets(cmd, target)) {
			if to == branch {
				continue
			}
			toKey := Scope(target, to)
			if toKey == "" || contains(toKeys, toKey) {
				continue
			}
			if reason := CheckLease(toKey, to, sid, S(rec, "role"), evCwd); reason != "" {
				return deny(reason)
			}
			toKeys = append(toKeys, toKey)
		}
	}
	// The verdicts above are in. Now the record may be written.
	fields := Rec{"turn_open": true}
	if !B(rec, "turn_open") {
		fields["turn_open_at"] = Now() // a tool call after Stop with no prompt is a new turn
	}
	if len(toKeys) > 0 {
		tos := make([]any, len(toKeys))
		for i, k := range toKeys {
			tos[i] = k
		}
		fields["handoff"] = Rec{"from": nilIfEmpty(key), "to": tos, "start": target, "at": Now()}
	}
	rec = TouchSession(sid, ev, fields)
	// A stop on a resource this lane requires stands the session down like a stop on
	// its branch.
	for _, r := range Strs(M(rec, "lane"), "requires") {
		if reason := CheckStop(r, r, sid); reason != "" {
			flag := StopFlag(r)
			TouchSession(sid, ev, Rec{"last_denied": Rec{"kind": "stop", "branch": r, "key": r, "at": Now(), "revoke": S(flag, "except") != ""}})
			RetireDeliveredStop(r, sid)
			return deny(reason)
		}
	}
	if reason := CheckRequires(rec, sid, tool, cmd); reason != "" {
		return deny(reason)
	}
	if tool != "Bash" {
		return allow
	}
	if reason := CheckCost(cmd, sid); reason != "" {
		return deny(reason)
	}
	ik := InflightKey(ev, sid, cmd)
	_ = WriteJSON(Path("inflight", Safe(ik)+".json"), Rec{"sig": Signature(cmd), "cmd": cut(cmd, 200), "at": Now(), "session": sid})
	if rule := MatchedRule(cmd); rule != nil {
		_ = WriteJSON(Path("locks", Safe(S(rule, "name"))+".json"), Rec{"session": sid, "at": Now(), "key": ik})
	}
	return allow
}

func onPostTool(ev Event, sid string) *Verdict {
	rec := TouchSession(sid, ev, Rec{})
	cmd := S(M(ev, "tool_input"), "command")
	if cmd != "" {
		start := S(ev, "cwd")
		if start == "" {
			start = S(rec, "cwd")
		}
		if start == "" {
			start = "."
		}
		CachePullRequest(cmd, ev["tool_response"], BashTarget(cmd, start), sid)
	}
	ik := InflightKey(ev, sid, cmd)
	p := Path("inflight", Safe(ik)+".json")
	started := ReadJSON(p)
	if started == nil {
		return allow
	}
	Unlink(p)
	elapsed := Now() - F(started, "at")
	sig := S(started, "sig")
	_ = AppendJSONL(Path("costs.jsonl"), Rec{"at": Now(), "session": sid, "sig": sig, "seconds": round1(elapsed)})
	if rule := MatchedRule(cmd); rule != nil {
		Unlink(Path("locks", Safe(S(rule, "name"))+".json"))
	}
	if elapsed < slowNoteS {
		return allow
	}
	med := ""
	for _, r := range CostRows() {
		if r.Sig == sig {
			med = fmt.Sprintf(" (median for `%s` here: %ds over %d runs)", sig, int(r.Med), r.N)
			break
		}
	}
	return context(ev, fmt.Sprintf("[fleet] that command took %s%s.", FmtAge(elapsed), med))
}

func round1(f float64) float64 {
	return float64(int64(f*10+0.5)) / 10
}

func onStop(ev Event, sid string) *Verdict {
	if B(ev, "stop_hook_active") {
		return allow
	}
	TouchSession(sid, ev, Rec{"turn_open": false, "last_stop_at": Now()})
	return allow
}

func onSessionEnd(ev Event, sid string) *Verdict {
	// Every branch lease is about to be released, so an unsettled switch needs no
	// settling here. The record write is fallible; the release is what keeps a closed
	// tab from reading as a live occupant, so it runs whatever the write did.
	func() {
		defer func() { _ = recover() }()
		TouchSession(sid, ev, Rec{"turn_open": false, "ended": true})
	}()
	ReleaseSessionState(sid)
	return allow
}

// Exit applies a verdict to the process.
func Exit(v *Verdict) {
	if v.Out != "" {
		_, _ = os.Stdout.WriteString(v.Out)
	}
	if v.Err != "" {
		_, _ = os.Stderr.WriteString(v.Err)
	}
	os.Exit(v.Code)
}
