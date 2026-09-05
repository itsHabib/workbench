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
	p := Path("sessions", sid+".json")
	rec := ReadJSON(p)
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
	if starting {
		role, _, slot := MapRowsFor(cwd)
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
		role, _, slot := MapRowsFor(cwd)
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
	_ = WriteJSON(p, rec)
	return rec
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
