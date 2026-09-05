package fleet

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Event is one harness hook event as decoded from stdin.
type Event = map[string]any

// TouchSession updates sessions/<sid>.json for this event and returns the record.
//
// Every SessionStart re-resolves the harness pid, including a resume: a session
// resumed under a new harness process keeps its session id, and a record that still
// named the dead pid would read as dead — and its branch lease taken over — while the
// session was live.
//
// The role is re-resolved at SessionStart and sticky between events within one
// session. At SessionStart the map's answer is taken as is, including none: an
// operator who deletes the wrong line must be able to strip a session of a role it
// should not have.
func TouchSession(sid string, ev Event, fields Rec) Rec {
	rec, _ := TouchErr(sid, ev, fields)
	return rec
}

// TouchErr is TouchSession with the publication error. A session record is evidence,
// not authority — except on the lease path, where a holder whose record cannot be
// read would be taken over by the next session; the caller there refuses.
func TouchErr(sid string, ev Event, fields Rec) (Rec, error) {
	// Under the session's own lock. The harness runs parallel tool calls, so two
	// PreToolUse hooks for one session run at once, and an unlocked read-modify-write
	// here let the later writer drop what the earlier one had just added — a handoff
	// in flight, a last_denied, turn_open. This is the second multi-writer object in
	// the store, keyed by session rather than by key, and it takes the same lock.
	// If the lock cannot be had in time the touch proceeds unlocked, as it always
	// did, rather than lose the event: a session record is evidence, not authority.
	var rec Rec
	var werr error
	err := KeyLock("session:"+sid, func() error {
		rec, werr = touchSessionLocked(sid, ev, fields)
		return nil
	})
	if err != nil {
		logError(Rec{"session": sid, "error": "touch_session unlocked: " + err.Error()})
		rec, werr = touchSessionLocked(sid, ev, fields)
	}
	return rec, werr
}

func touchSessionLocked(sid string, ev Event, fields Rec) (Rec, error) {
	p := Path("sessions", sid+".json")
	rec := ReadJSON(p)
	TestPause("IN_TOUCH") // between the read and the write; a rival's write must not be lost
	name := S(ev, "hook_event_name")
	starting := name == "SessionStart"
	if rec == nil {
		pid, kind := HarnessPid(starting)
		rec = Rec{"session": sid, "started_at": Now(), "pid": float64(pid), "pid_kind": kind}
	} else if starting {
		pid, kind := HarnessPid(true)
		rec["pid"], rec["pid_kind"] = float64(pid), kind
	}
	for k, v := range fields {
		rec[k] = v
	}
	rec["last_event"], rec["last_event_at"] = name, Now()
	if cwd := S(ev, "cwd"); cwd != "" {
		rec["cwd"] = cwd
	} else if !Has(rec, "cwd") {
		rec["cwd"] = nil
	}
	cwd := S(rec, "cwd")
	// Identity comes from the directory the session was LAUNCHED in, recorded once
	// at SessionStart. The role used to re-resolve from the event's cwd whenever it
	// was empty, so a session that started unroled in ~/dev and ran one
	// `cd ~/dev/cc-skills && …` came out wearing that checkout's card. A `cd` is not
	// a change of who you are.
	if starting {
		rec["launch_dir"] = nilIfEmpty(cwd)
	}
	launch := S(rec, "launch_dir")
	if launch == "" {
		launch = cwd // a record from before launch_dir existed
	}
	if starting {
		role, _, slot := MapRowsFor(launch)
		rec["role"], rec["slot"] = nilIfEmpty(role), nilIfEmpty(slot)
		lane := LaneOf(role)
		if lane == nil {
			rec["lane"] = nil
		} else {
			rec["lane"] = lane
		}
		if role != "" && lane == nil {
			logError(Rec{"session": sid, "error": fmt.Sprintf("no manifest for role %s under %s; requires/produces unchecked", role, LanesDir())})
		}
	}
	if S(rec, "role") == "" {
		role, _, slot := MapRowsFor(launch)
		rec["role"], rec["slot"] = nilIfEmpty(role), nilIfEmpty(slot)
		if role != "" {
			if lane := LaneOf(role); lane != nil {
				rec["lane"] = lane
			} else {
				rec["lane"] = nil
			}
		}
	}
	if cwd != "" {
		rec["branch"] = nilIfEmpty(BranchOf(cwd))
		rec["repo"] = nilIfEmpty(RepoID(cwd))
	}
	return rec, WriteJSON(p, rec)
}

// InflightKey is the key PreToolUse writes and PostToolUse reads for one command:
// the harness's tool_use_id when it sends one, else a digest of the command.
func InflightKey(ev Event, sid, cmd string) string {
	if id := S(ev, "tool_use_id"); id != "" {
		return id
	}
	return sid + "-" + sha1hex(cmd)[:16]
}

// RetireDeliveredStop retires a revoke's flag once it has been delivered to the
// session it displaced. Only that session's denial retires it: a bystander refused by
// the same flag must not consume the signal meant for someone else. A flag with no
// recorded holder had nobody to stand down and retires on first delivery. A plain
// `fleet stop` has no `except` and stands until `fleet resume`.
func RetireDeliveredStop(key, sid string) {
	flag := StopFlag(key)
	if flag == nil || S(flag, "except") == "" {
		return
	}
	if h := S(flag, "holder"); h != "" && h != sid {
		return
	}
	Unlink(KeyFile("stop", key))
}

// OpenDecisions is the operator decisions in force. A `close` row retires an id.
func OpenDecisions() []Rec {
	text, ok := readText(Path("decisions.jsonl"))
	if !ok {
		return nil
	}
	rows := map[string]Rec{}
	var order []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		r := ReadJSONBytes([]byte(line))
		if r == nil {
			return nil
		}
		id := S(r, "id")
		if S(r, "kind") == "close" {
			delete(rows, id)
			continue
		}
		if _, seen := rows[id]; !seen {
			order = append(order, id)
		}
		rows[id] = r
	}
	var out []Rec
	for _, id := range order {
		if r, ok := rows[id]; ok {
			out = append(out, r)
		}
	}
	return out
}

// DecisionsLine renders the decisions in force for injection, byte-capped.
func DecisionsLine() string {
	ds := OpenDecisions()
	if len(ds) == 0 {
		return ""
	}
	var parts []string
	for _, d := range ds {
		parts = append(parts, strings.ReplaceAll(fmt.Sprintf("%s %s %s: %s", S(d, "id"), S(d, "kind"), S(d, "subject"), S(d, "text")), "  ", " "))
	}
	line := "[fleet] operator decisions in force (a `drop` subject is not work; cite the id when you act on one): " + strings.Join(parts, " · ")
	if len(line) > 700 {
		line = line[:700]
	}
	return line
}

// HandoffLine is the branch's last handoff: one line, replaced not appended.
func HandoffLine(key, branch string) string {
	if key == "" {
		return ""
	}
	r := ReadJSON(KeyFile("handoff", key))
	if r == nil {
		return ""
	}
	nxt := ""
	if n := S(r, "next"); n != "" {
		nxt = " · next: " + n
	}
	sha := S(r, "sha")
	if sha == "" {
		sha = "?"
	}
	return fmt.Sprintf("[fleet] last handoff on %s (%s ago at %s): %s%s", branch, FmtAge(Now()-F(r, "at")), Short(sha), S(r, "conclusion"), nxt)
}

// CostRow is one cost-ledger aggregate: signature, median seconds, count.
type CostRow struct {
	Sig string
	Med float64
	N   int
}

// CostRows folds costs.jsonl to per-signature medians, slowest first.
func CostRows() []CostRow {
	text, ok := readText(Path("costs.jsonl"))
	if !ok {
		return nil
	}
	rows := map[string][]float64{}
	var order []string
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		r := ReadJSONBytes([]byte(line))
		if r == nil {
			return nil
		}
		sig := S(r, "sig")
		if _, seen := rows[sig]; !seen {
			order = append(order, sig)
		}
		rows[sig] = append(rows[sig], F(r, "seconds"))
	}
	var out []CostRow
	for _, sig := range order {
		xs := rows[sig]
		sortFloats(xs)
		out = append(out, CostRow{sig, xs[len(xs)/2], len(xs)})
	}
	stableSort(out, func(a, b CostRow) bool { return a.Med > b.Med })
	return out
}

// AssignmentLine is what `fleet assign` placed into this slot, for the session that
// just opened in it. Delivery is stamped on the record by this read, which is the one
// action that consumes an assignment.
func AssignmentLine(slot, sid string) string {
	if slot == "" {
		return ""
	}
	p := Path("assign", Safe(slot)+".json")
	a := ReadJSON(p)
	if a == nil || S(a, "branch") == "" {
		return ""
	}
	if d := S(a, "delivered_to"); d != "" && d != sid {
		return ""
	}
	if sid != "" && S(a, "delivered_to") == "" {
		a["delivered_to"], a["delivered_at"] = sid, Now()
		_ = WriteJSON(p, a)
	}
	brief := strings.Join(strings.Fields(S(a, "brief")), " ")
	if len(brief) > 300 {
		brief = brief[:300]
	}
	tail := ""
	if brief != "" {
		by := S(a, "by")
		if by == "" {
			by = "operator"
		}
		tail = fmt.Sprintf(`. The dispatcher (%s) wrote, quoted and not a fleet rule: "%s"`, by, brief)
	}
	return fmt.Sprintf("[fleet] this slot was assigned %s ago: branch %s%s", FmtAge(Now()-F(a, "at")), S(a, "branch"), tail)
}

// RecordLastWord captures the session's conclusion at Stop with no act by the agent:
// the final assistant message, read from the transcript the event names, keyed by the
// work (the branch) so a successor on that branch finds it at SessionStart.
//
// This is the observed form of the one declaration the design had kept. A declared
// conclusion — `fleet handoff` — is written only when an agent remembers, and the
// chain shows nineteen mechanical marks for every checkpoint anyone wrote; it also
// cannot exist for the sessions that died, which is exactly when a cold pickup matters
// most. The last word exists for every session that ever stopped. `fleet handoff`
// stays as the better, declared version when an agent bothers.
func RecordLastWord(sid string, ev Event, rec Rec) {
	// The harness's own statement of the final message first; else its transcript.
	text := collapse(S(ev, "last_assistant_message"))
	if text == "" {
		text = lastAssistantMessage(S(ev, "transcript_path"))
	}
	if text == "" {
		return
	}
	cwd := S(rec, "cwd")
	branch := S(rec, "branch")
	if cwd == "" || branch == "" {
		return
	}
	key := Scope(cwd, branch)
	if key == "" {
		return
	}
	_ = WriteJSON(KeyFile("last-word", key), Rec{"key": key, "branch": branch, "repo": nilIfEmpty(S(rec, "repo")),
		"session": sid, "role": nilIfEmpty(S(rec, "role")), "head": nilIfEmpty(headSha(cwd)), "at": Now(), "text": text})
}

// lastAssistantMessage is the text of the last assistant turn in a harness transcript
// (JSONL, one event per line), whitespace-collapsed and capped. "" when there is none.
func lastAssistantMessage(transcript string) string {
	if transcript == "" {
		return ""
	}
	raw, ok := readText(transcript)
	if !ok {
		return ""
	}
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if text := collapse(assistantText(ReadJSONBytes([]byte(lines[i])))); text != "" {
			return text
		}
	}
	return ""
}

// assistantText is the text of one transcript line when it is an assistant message,
// in either harness's shape: Claude's top-level {type: assistant, message: {content}},
// or Codex's {type: response_item, payload: {type: message, role: assistant, content}}.
func assistantText(r Rec) string {
	if r == nil {
		return ""
	}
	var content any
	switch S(r, "type") {
	case "assistant":
		content = M(r, "message")["content"]
	case "response_item":
		p := M(r, "payload")
		if S(p, "type") != "message" || S(p, "role") != "assistant" {
			return ""
		}
		content = p["content"]
	default:
		return ""
	}
	var parts []string
	switch c := content.(type) {
	case string:
		parts = append(parts, c)
	case []any:
		for _, block := range c {
			b, ok := block.(map[string]any)
			if !ok {
				continue
			}
			if t := S(b, "type"); t == "text" || t == "output_text" {
				parts = append(parts, S(b, "text"))
			}
		}
	}
	return strings.Join(parts, " ")
}

// collapse is whitespace-folded text, capped for injection.
func collapse(s string) string {
	text := strings.Join(strings.Fields(s), " ")
	if len(text) > 600 {
		text = text[:600] + "…"
	}
	return text
}

// headSha is the commit the checkout's branch points at, read from refs without a
// spawn; "" when it cannot be read.
func headSha(start string) string {
	branch := BranchOf(start)
	_, common := GitDirs(start)
	if branch == "" || common == "" {
		return ""
	}
	if sha, ok := readText(filepath.Join(append([]string{common, "refs", "heads"}, strings.Split(branch, "/")...)...)); ok {
		return strings.TrimSpace(sha)
	}
	packed, _ := readText(filepath.Join(common, "packed-refs"))
	for _, line := range strings.Split(packed, "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == "refs/heads/"+branch {
			return f[0]
		}
	}
	return ""
}

// LastWordLine is the branch's last word for injection at SessionStart, or "".
func LastWordLine(key, branch, sid string) string {
	if key == "" {
		return ""
	}
	r := ReadJSON(KeyFile("last-word", key))
	if r == nil || S(r, "text") == "" {
		return ""
	}
	who := Short(S(r, "session"))
	if S(r, "session") == sid {
		who = "you, last time"
	}
	text := S(r, "text")
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	return fmt.Sprintf("[fleet] last word on %s (%s ago, %s): %s", branch, FmtAge(Now()-F(r, "at")), who, text)
}

// BoardLines is the watcher's board for a lane that reads it at every prompt: the rows
// needing a decision, and what changed since this session's last prompt — so a hub
// answers "how is it going" from words already in its context instead of rebuilding
// state from the network each time. Attention-budgeted: a few lines, capped, and "all
// fine" is one line. When no watcher has ticked, that is said rather than hidden.
func BoardLines(sincePrompt float64) []string {
	hb := ReadJSON(Path("watch", "heartbeat.json"))
	if hb == nil {
		return []string{"[fleet] no board: the watcher has never ticked here (`fleet watch --once` to see one now)"}
	}
	age := Now() - F(hb, "at")
	iv := F(hb, "interval")
	if iv <= 0 {
		iv = 60
	}
	var lines []string
	if age > 2*iv {
		lines = append(lines, fmt.Sprintf("[fleet] board is %s stale: the watcher is not ticking (any SessionStart revives it)", FmtAge(age)))
	}
	rows, _ := readAny(Path("watch", "board.json")).([]any)
	var need []string
	fine := 0
	for _, raw := range rows {
		r, _ := raw.(map[string]any)
		st := S(r, "state")
		switch st {
		case "busy-and-overdue", "dead-holding-work", "assigned-no-occupant", "unknown":
			who := Short(S(r, "session"))
			name := S(r, "slot")
			if name == "" {
				name = filepath.Base(strings.TrimRight(S(r, "path"), "/"))
			}
			need = append(need, fmt.Sprintf("%s %s %s (%s)", st, S(r, "role"), name, who))
		default:
			fine++
		}
	}
	// Ownership rows: the ones needing a decision are named; the rest are a count.
	work, _ := readAny(Path("watch", "work.json")).([]any)
	fineWork := 0
	for _, raw := range work {
		r, _ := raw.(map[string]any)
		st := S(r, "state")
		if st != "dead" && st != "late" && st != "undeclared" && st != "abandoned" && st != "failed" {
			fineWork++
			continue
		}
		name := S(r, "change")
		if rel := S(r, "relationship"); rel != "" {
			name += "/" + rel
		}
		acc := S(r, "for")
		if acc == "" {
			acc = "no one accountable"
		}
		who := "nobody"
		if h := S(r, "hands"); h != "" {
			who = Short(h)
		}
		need = append(need, fmt.Sprintf("%s %s for %s (hands %s)", st, name, acc, who))
	}
	if len(need) > 0 {
		line := fmt.Sprintf("[fleet] board (%s ago): %d need a decision — %s", FmtAge(age), len(need), strings.Join(need, "; "))
		if len(line) > 700 {
			line = line[:700] + "…"
		}
		lines = append(lines, line)
	} else if len(rows) > 0 || fineWork > 0 {
		lines = append(lines, fmt.Sprintf("[fleet] board (%s ago): nothing needs a decision; %d fine, %d work rows fine", FmtAge(age), fine, fineWork))
	}
	// Transitions since this session's last prompt, newest last, capped.
	if sincePrompt > 0 {
		text, _ := readText(Path("watch", "observed.jsonl"))
		var changes []string
		for _, l := range strings.Split(text, "\n") {
			t := ReadJSONBytes([]byte(l))
			if t == nil || F(t, "at") <= sincePrompt {
				continue
			}
			from := S(t, "from")
			if from == "" {
				from = "—"
			}
			if c := S(t, "change"); c != "" {
				if rel := S(t, "relationship"); rel != "" {
					c += "/" + rel
				}
				acc := S(t, "for")
				if acc == "" {
					acc = "—"
				}
				if what := S(t, "what"); what != "" {
					changes = append(changes, fmt.Sprintf("%s %s %s: %s → %s", acc, c, what, from, S(t, "to")))
					continue
				}
				changes = append(changes, fmt.Sprintf("%s %s: %s → %s", acc, c, from, S(t, "to")))
				continue
			}
			name := S(t, "slot")
			if name == "" {
				name = filepath.Base(strings.TrimRight(S(t, "path"), "/"))
			}
			changes = append(changes, fmt.Sprintf("%s %s: %s → %s", S(t, "role"), name, from, S(t, "to")))
		}
		if n := len(changes); n > 0 {
			if n > 8 {
				changes = append(changes[n-8:], fmt.Sprintf("(+%d earlier)", n-8))
			}
			line := "[fleet] since your last prompt: " + strings.Join(changes, "; ")
			if len(line) > 700 {
				line = line[:700] + "…"
			}
			lines = append(lines, line)
		}
	}
	return lines
}

// PullFile is prs/<repo-id>__<n>.json.
func PullFile(rid string, number int) string {
	return Path("prs", fmt.Sprintf("%s__%d.json", rid, number))
}

// occupySlot binds this session to the pooled worktree it started in: the lease on
// `slot:<name>` is the name table, written by the hook at SessionStart, never by an
// agent. Returns a line for the session when the seat is contested.
//
// A dead occupant is displaced here without ceremony; a dead holder of a MACHINE
// resource is not. The asymmetry is about the cost of being wrong: leaving a machine
// advertised as free while it may still be driving is the collision the slot exists
// to prevent; leaving a worktree orphaned costs a seat, and a seat is cheap. A LIVE
// occupant is reported, not displaced — SessionStart has no verdict to deny with.
func occupySlot(sid, slot, role, cwd string) string {
	key := "slot:" + slot
	var line string
	var displaced Rec
	err := KeyLock(key, func() error {
		cur := Lease(key)
		if IsMalformed(cur) {
			line = fmt.Sprintf("[fleet] slot %s: its lease file is malformed (%s); `fleet who %s` cannot resolve it", slot, S(cur, "malformed"), slot)
			return nil
		}
		if cur != nil && !B(cur, "occupancy") {
			role := S(cur, "role")
			if role == "" {
				role = "session"
			}
			sess := S(cur, "session")
			if sess == "" {
				sess = "?"
			}
			line = fmt.Sprintf("[fleet] slot %s: slot:%s is a resource lease held by %s %s, not a seat; this worktree's name collides with a machine. `fleet who %s` will not resolve to you until it is renamed.", slot, slot, role, Short(sess), slot)
			return nil
		}
		if cur != nil && S(cur, "session") != sid {
			holder := ReadJSON(Path("sessions", S(cur, "session")+".json"))
			if SessionAlive(holder) {
				role := S(cur, "role")
				if role == "" {
					role = "session"
				}
				line = fmt.Sprintf("[fleet] slot %s is already occupied by %s %s (since %s ago). Two sessions in one worktree; one of you should end.", slot, role, Short(S(cur, "session")), FmtAge(Now()-F(cur, "since")))
				return nil
			}
			displaced = cur
		}
		rec := LeaseRecord(key, sid, role, cwd, "occupied at SessionStart")
		rec["occupancy"] = true
		return WriteLease(key, rec)
	})
	if err == ErrKeyBusy {
		return fmt.Sprintf("[fleet] slot %s: its lock was busy; `fleet who %s` may lag one event", slot, slot)
	}
	if line != "" {
		return line
	}
	if displaced != nil {
		return fmt.Sprintf("[fleet] slot %s: took the seat from dead session %s (last seen %s ago). A process it left running — a build, a watcher, a dev server — may still be writing here; check before you rely on the tree.", slot, Short(S(displaced, "session")), FmtAge(Now()-F(displaced, "since")))
	}
	return ""
}

// canonPath is one directory identity for comparing a recorded cwd with a live one:
// symlinks resolved, long-form, forward-slashed, case-folded where the filesystem is.
func canonPath(p string) string {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		real = p
	}
	return NormCase(LongPath(real))
}

// CanonPath is canonPath, exported for the verbs.
func CanonPath(p string) string { return canonPath(p) }

// Within reports whether p is dir or a path-component descendant of it, on either
// separator: CanonPath keeps the platform's, and a test that appends "/" misses every
// Windows descendant.
func Within(p, dir string) bool {
	a := strings.TrimRight(canonPath(p), `/\`)
	b := strings.TrimRight(canonPath(dir), `/\`)
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+"/") || strings.HasPrefix(a, b+`\`)
}
