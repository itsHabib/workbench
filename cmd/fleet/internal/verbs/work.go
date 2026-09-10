package verbs

// Work: the ownership row, the unit a hub reasons about.
//
// A row is one (change, relationship). Its declared part is written once, by
// `fleet dispatch`: the change (a branch), the relationship (a word the hub picks —
// the substrate does not enumerate kinds), who is accountable (`for`, a role), who
// dispatched (`by`), when it is due, and which slot it was placed in. Everything
// else is observed at read time: who has hands on it (the branch lease and its
// holder's liveness), and whether it is done (a passing receipt whose kind is the
// relationship, at the branch head). States are computed, never set:
//
//	dispatched   declared, nobody has hands on it yet
//	working      hands alive, turn open
//	idle         hands alive, turn closed
//	late         past due and not done (hands or not)
//	dead         the hands' session is gone
//	done         a passing receipt of the relationship's kind at the head
//	undeclared   a live session holds the branch and no row declares it
//
// Splitting a hub is `fleet reassign --for <role> <change>`: `for` is a column, not
// a tree, so nothing else moves. Undeclared work is a visible state, not an error.
//
// This is the local half of the record. The change's remote record (the same row as
// a sticky comment on its pull request, readable from any machine) is a later rung;
// `fleet work` says so in its scope line until then.

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// WorkRow is one ownership row with its observed state.
type WorkRow = map[string]any

var relationshipRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

func dispatchDir() string { return fleet.Path("dispatch") }

func dispatchFile(rid, branch, rel string) string {
	return filepath.Join(dispatchDir(), fleet.Safe(rid+"__"+branch+"__"+rel)+".json")
}

func dispatchRows() []fleet.Rec {
	ents, _ := os.ReadDir(dispatchDir())
	var out []fleet.Rec
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		r := fleet.ReadJSON(filepath.Join(dispatchDir(), e.Name()))
		if r != nil && fleet.S(r, "change") != "" && fleet.S(r, "repo") != "" && fleet.S(r, "relationship") != "" {
			out = append(out, r)
		}
	}
	sortBy(out, func(a, b fleet.Rec) bool { return fleet.F(a, "at") < fleet.F(b, "at") })
	return out
}

// resolveDispatchTarget is (repo id, branch, head) for a change named by branch or
// #n, from inside a checkout of its repo.
func resolveDispatchTarget(verb, change string) (string, string, string, error) {
	rid := fleet.RepoID(cwd())
	if rid == "" {
		return "", "", "", refuse("fleet %s: %s is not inside a git repo; run this inside a checkout of the change's repo", verb, cwd())
	}
	sha, branch, _, err := resolveChange(change)
	if err != nil {
		return "", "", "", err
	}
	if branch == "" {
		return "", "", "", refuse("fleet %s: %s names a revision, not a change; name a branch or #n", verb, fleet.PyRepr(change))
	}
	return rid, branch, sha, nil
}

// liveHands is the live session holding a branch key, or "".
func liveHands(key string) string {
	cur := fleet.Lease(key)
	if cur == nil || fleet.IsMalformed(cur) || fleet.B(cur, "occupancy") {
		return ""
	}
	sid := fleet.S(cur, "session")
	if !fleet.SessionAlive(fleet.SessionRecord(sid)) {
		return ""
	}
	return sid
}

// CmdDispatch is the one declared act: write the row, and place the work when a
// slot is named. Placement is `assign`, under the slot's lock, before the row is
// written, so a refused placement leaves no row behind.
func CmdDispatch(change, rel, forRole, due, slot, brief, by, replyTo string, take bool) error {
	return fleet.KeyLock("dispatch", func() error {
		return cmdDispatch(change, rel, forRole, due, slot, brief, by, replyTo, take)
	})
}

func cmdDispatch(change, rel, forRole, due, slot, brief, by, replyTo string, take bool) error {
	usage := `usage: fleet dispatch <branch|#n> --as <relationship> [--for <role>] [--due 45m] [--slot <name>] [--brief "<one line>"] [--reply-to <session>] [--take]`
	if change == "" || rel == "" {
		return refuse("%s", usage)
	}
	if !relationshipRe.MatchString(rel) {
		return refuse("fleet dispatch: --as wants a short lowercase word (the receipt kind that means done), got %s", fleet.PyRepr(rel))
	}
	var dueSecs float64
	if due != "" {
		dueSecs = fleet.ParseDuration(due)
		if dueSecs == 0 {
			return refuse("fleet dispatch: --due wants a duration like 45m or 2h, got %s", fleet.PyRepr(due))
		}
	}
	rid, branch, sha, err := resolveDispatchTarget("dispatch", change)
	if err != nil {
		return err
	}
	by = dispatcher(by)
	if forRole == "" && by == "mcp" {
		return refuse("fleet dispatch: %s has no role bound, so the dispatching hub cannot be told from the call; pass for=<role>", cwd())
	}
	if forRole == "" {
		forRole = by
	}
	key := "repo:" + rid + ":" + branch
	existing, err := replaceableDispatch(rid, branch, rel)
	if err != nil {
		return err
	}
	if hands := liveHands(key); hands != "" && existing != nil && !take {
		return refuse("fleet dispatch: %s/%s already has live hands (%s, for %s); `--take` rewrites the row, or `fleet work` to see it",
			branch, rel, fleet.Short(hands), fleet.S(existing, "for"))
	}
	if slot != "" {
		if err := CmdAssign(slot, branch, brief, by, forRole, replyTo); err != nil {
			return err
		}
	}
	now := fleet.Now()
	rec := fleet.Rec{"change": branch, "repo": rid, "relationship": rel, "for": forRole, "by": by, "at": now,
		"due": nil, "slot": nilIfEmpty(slot), "brief": nilIfEmpty(strings.TrimSpace(brief)), "reply_to": nilIfEmpty(replyTo), "head_at_dispatch": nilIfEmpty(sha)}
	if dueSecs > 0 {
		rec["due"] = now + dueSecs
	}
	if err := fleet.WriteJSON(dispatchFile(rid, branch, rel), rec); err != nil {
		return err
	}
	fleet.ObserveAction("dispatch", rec)
	tail := ""
	if dueSecs > 0 {
		tail += ", due in " + fleet.FmtAge(dueSecs)
	}
	if slot != "" {
		tail += ", in " + slot
	}
	say("dispatched %s/%s for %s%s; %s", branch, rel, forRole, tail, upsertOwnership(rid, branch))
	return nil
}

// dispatcher is who is acting: the role bound to the cwd when there is one; else the
// operator at a terminal, or the transport's name for an MCP call with no role — which
// is never allowed to become the accountable column.
func dispatcher(by string) string {
	if role := fleet.RoleOf(cwd()); role != "" {
		return role
	}
	if by == "" {
		return "operator"
	}
	return by
}

// CmdReassign moves every row of a change to another accountable role. Two
// commands split a hub: bind the second role's directory, then reassign its rows.
func CmdReassign(change, forRole string) error {
	return fleet.KeyLock("dispatch", func() error { return cmdReassign(change, forRole) })
}

func cmdReassign(change, forRole string) error {
	if change == "" || forRole == "" {
		return refuse("usage: fleet reassign <branch|#n> --for <role>")
	}
	rid, branch, _, err := resolveDispatchTarget("reassign", change)
	if err != nil {
		return err
	}
	rows, err := scopedDispatchRows(rid, branch, "")
	if err != nil {
		return err
	}
	for _, r := range rows {
		if fleet.S(r, "repo") == rid && fleet.S(r, "change") == branch && fleet.S(r, "request_id") != "" {
			return refuse("fleet: request-bound assignments require a correlated lifecycle action; inspect `fleet status`")
		}
	}
	n := 0
	for _, r := range rows {
		if fleet.S(r, "repo") != rid || fleet.S(r, "change") != branch {
			continue
		}
		r["for"] = forRole
		r["reassigned_at"] = fleet.Now()
		if err := fleet.WriteJSON(dispatchFile(rid, branch, fleet.S(r, "relationship")), r); err != nil {
			return err
		}
		fleet.ObserveAction("reassign", r)
		n++
	}
	if n == 0 {
		return refuse("fleet reassign: no row for %s here; `fleet dispatch` declares one", branch)
	}
	say("%s: %d row(s) now for %s; %s", branch, n, forRole, upsertOwnership(rid, branch))
	return nil
}

// cmdUndispatch retires a change's rows (one relationship, or all of them).
func cmdUndispatch(change, rel string) error {
	return fleet.KeyLock("dispatch", func() error { return undispatch(change, rel) })
}

func undispatch(change, rel string) error {
	if change == "" {
		return refuse("usage: fleet undispatch <branch|#n> [--as <relationship>]")
	}
	rid, branch, _, err := resolveDispatchTarget("undispatch", change)
	if err != nil {
		return err
	}
	rows, err := scopedDispatchRows(rid, branch, rel)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if fleet.S(r, "repo") == rid && fleet.S(r, "change") == branch && (rel == "" || fleet.S(r, "relationship") == rel) && fleet.S(r, "request_id") != "" {
			return refuse("fleet: request-bound assignments require a correlated lifecycle action; inspect `fleet status`")
		}
	}
	n := 0
	for _, r := range rows {
		if fleet.S(r, "repo") != rid || fleet.S(r, "change") != branch || (rel != "" && fleet.S(r, "relationship") != rel) {
			continue
		}
		fleet.Unlink(dispatchFile(rid, branch, fleet.S(r, "relationship")))
		n++
	}
	if n == 0 {
		return refuse("fleet undispatch: no row for %s here", branch)
	}
	say("%s: %d row(s) retired; %s", branch, n, upsertOwnership(rid, branch))
	return nil
}

// branchHead is the head a branch names in some checkout of rid, or "".
func branchHead(rid, branch string) string {
	sha, _, _, err := resolveBranchHead(rid, branch, "")
	if err != nil {
		return ""
	}
	return sha
}

// WorkRows is every ownership row with its observed state, attention first.
func WorkRows(forRole string) []WorkRow {
	now := fleet.Now()
	declared := map[string]bool{}
	sessions := sessionRows()
	var rows []WorkRow
	for _, d := range dispatchRows() {
		rid, branch, rel := fleet.S(d, "repo"), fleet.S(d, "change"), fleet.S(d, "relationship")
		declared["repo:"+rid+":"+branch] = true
		declared[rid+"|"+branch+"|"+rel] = true
		rows = append(rows, declaredRow(d, sessions, now))
	}
	rows = append(rows, undeclaredRows(declared)...)
	rows = append(rows, remoteRows(declared, now)...)
	if forRole != "" {
		var mine []WorkRow
		for _, r := range rows {
			if fleet.S(r, "for") == forRole {
				mine = append(mine, r)
			}
		}
		rows = mine
	}
	order := map[string]int{"dead": 0, "failed": 1, "abandoned": 2, "late": 3, "undeclared": 4, "working": 5, "idle": 6, "dispatched": 7, "remote": 8, "done": 9}
	sortBy(rows, func(a, b WorkRow) bool {
		oa, ob := order[fleet.S(a, "state")], order[fleet.S(b, "state")]
		if oa != ob {
			return oa < ob
		}
		return fleet.S(a, "change") < fleet.S(b, "change")
	})
	return rows
}

// declaredRow is one dispatched row with its observed state: hands from the branch
// lease, done or failed from the latest receipt of its kind, late from its due.
func declaredRow(d fleet.Rec, sessions []fleet.Rec, now float64) WorkRow {
	rid, branch, rel := fleet.S(d, "repo"), fleet.S(d, "change"), fleet.S(d, "relationship")
	key := "repo:" + rid + ":" + branch
	row := WorkRow{"change": branch, "repo": rid, "relationship": rel, "for": d["for"], "by": d["by"], "at": d["at"],
		"due": d["due"], "slot": d["slot"], "brief": d["brief"], "key": key, "hands": nil, "state": "dispatched", "head": nil, "done_at": nil}
	state := handsState(row, key)
	if state == "dispatched" {
		// No hands now. A session that left with the branch is not "never started".
		for _, s := range sessions {
			if fleet.S(s, "branch") == branch && fleet.S(s, "repo") == rid && !fleet.SessionAlive(s) && fleet.F(s, "last_event_at") > fleet.F(d, "at") {
				state = "abandoned"
				row["left"] = s["session"]
				break
			}
		}
	}
	state = evidenceState(row, rid, branch, rel, state)
	if due := fleet.F(d, "due"); due > 0 && now > due && (state == "dispatched" || state == "working" || state == "idle") {
		state = "late"
	}
	row["state"] = state
	return row
}

// handsState is who holds the branch and whether they are alive and mid-turn.
func handsState(row WorkRow, key string) string {
	cur := fleet.Lease(key)
	if cur == nil || fleet.IsMalformed(cur) || fleet.B(cur, "occupancy") {
		return "dispatched"
	}
	sid := fleet.S(cur, "session")
	row["hands"] = sid
	srec := fleet.SessionRecord(sid)
	switch {
	case !fleet.SessionAlive(srec):
		return "dead"
	case fleet.B(srec, "turn_open"):
		return "working"
	default:
		return "idle"
	}
}

// evidenceState applies the latest receipt of the relationship's kind at the head:
// pass is done, fail is failed (unless the holder is dead, which comes first).
func evidenceState(row WorkRow, rid, branch, rel, state string) string {
	head := branchHead(rid, branch)
	if head == "" {
		return state
	}
	row["head"] = head
	for _, r := range receiptRows(head, rel, 0, false) {
		if fleet.S(r, "malformed") != "" {
			continue
		}
		// Rows are newest first: the latest receipt of the kind decides.
		if fleet.S(r, "verdict") == "pass" {
			row["done_at"] = r["at"]
			return "done"
		}
		if state != "dead" {
			row["failed_at"] = r["at"]
			return "failed"
		}
		return state
	}
	return state
}

// undeclaredRows is every branch a session holds that no row declares: undeclared
// while the holder lives, dead once it does not.
//
// `undeclared` means no ownership ROW exists, which is not the same as nobody being
// accountable. `fleet assign --for <role>` records the accountable role, the dispatcher,
// the seat and the brief in the assignment; reading none of that made the board print
// "for no one accountable" about a change whose own assignment named the role — two
// records fleet wrote, disagreeing. The STATE stays `undeclared`, which was right: a seat
// assignment is not an ownership declaration. The columns now come from the assignment
// when there is one.
func undeclaredRows(declared map[string]bool) []WorkRow {
	var rows []WorkRow
	for _, l := range leaseRows() {
		key := fleet.S(l, "key")
		if fleet.B(l, "occupancy") || fleet.IsResource(key) || declared[key] {
			continue
		}
		sid := fleet.S(l, "session")
		state := "undeclared"
		if !fleet.SessionAlive(fleet.SessionRecord(sid)) {
			state = "dead" // a dead holder nobody declared is still a dead holder
		}
		parts := fleet.KeyParts(key)
		repo, branch := fleet.S(parts, "repo"), fleet.S(parts, "branch")
		row := WorkRow{"change": branch, "repo": repo, "relationship": nil, "for": nil, "by": nil,
			"at": l["at"], "due": nil, "slot": nil, "brief": nil, "key": key, "hands": sid, "state": state, "head": nil, "done_at": nil}
		if a := holderAssignment(repo, branch, sid); a != nil {
			row["for"] = nilIfEmpty(fleet.S(a, "for"))
			row["by"] = nilIfEmpty(fleet.S(a, "by"))
			row["slot"] = nilIfEmpty(fleet.S(a, "slot"))
			row["brief"] = nilIfEmpty(fleet.S(a, "brief"))
		}
		rows = append(rows, row)
	}
	return rows
}

// holderAssignment reads the holder's seat, never a branch-wide collapse of
// assignments left behind in other seats. Delivery stamps record the first reader;
// replacement sessions inherit the still-current placement, not that reader's ID.
func holderAssignment(repo, branch, sid string) fleet.Rec {
	rec := fleet.SessionRecord(sid)
	slot := fleet.S(rec, "slot")
	launch := fleet.S(rec, "launch_dir")
	if launch == "" {
		launch = fleet.S(rec, "cwd")
	}
	role, tenant, boundSlot := fleet.MapRowsFor(launch)
	if slot == "" || boundSlot != slot || fleet.RepoID(launch) != repo || fleet.BranchOf(launch) != branch {
		return nil
	}
	if fleet.S(rec, "repo") != repo || fleet.S(rec, "branch") != branch {
		return nil
	}
	a := fleet.ReadJSON(fleet.Path("assign", fleet.Safe(slot)+".json"))
	if role == "" || tenant == "" || fleet.S(a, "role") != role || fleet.S(a, "tenant") != tenant {
		return nil
	}
	if fleet.S(a, "repo") != repo || fleet.S(a, "branch") != branch || fleet.S(a, "slot") != slot {
		return nil
	}
	if fleet.S(a, "path") == "" || canon(fleet.S(a, "path")) != canon(launch) {
		return nil
	}
	// A new assignment can reuse the same branch and seat after the old session
	// dies. It cannot establish accountability for that session's earlier work.
	if fleet.F(a, "at") > fleet.F(rec, "last_event_at") {
		return nil
	}
	return a
}

// WorkAttention is the set of work states a hub must decide something about.
var WorkAttention = map[string]bool{"dead": true, "late": true, "undeclared": true, "abandoned": true, "failed": true, "unknown": true}

// WorkLine is one row as a hub reads it.
func WorkLine(r WorkRow, now float64) string {
	name := fleet.S(r, "change")
	if rel := fleet.S(r, "relationship"); rel != "" {
		name += "/" + rel
	}
	who := "nobody"
	if h := fleet.S(r, "hands"); h != "" {
		who = fleet.Short(h)
	}
	if l := fleet.S(r, "left"); l != "" {
		who = fleet.Short(l) + " left"
	}
	acc := fleet.S(r, "for")
	if acc == "" {
		acc = "no one accountable"
	}
	tail := ""
	if due := fleet.F(r, "due"); due > 0 {
		if now > due {
			tail = fmt.Sprintf(", due %s ago", fleet.FmtAge(now-due))
		} else {
			tail = fmt.Sprintf(", due in %s", fleet.FmtAge(due-now))
		}
	}
	if s := fleet.S(r, "slot"); s != "" {
		tail += ", in " + s
	}
	if m := fleet.S(r, "machine"); m != "" {
		who = "on " + m
		tail += fmt.Sprintf(", cache %s old", fleet.FmtAge(now-fleet.F(r, "cache_at")))
	}
	return fmt.Sprintf("%s %s for %s (hands %s%s)", fleet.S(r, "state"), name, acc, who, tail)
}

func cmdWork(forRole string, asJSON bool) error {
	rows := WorkRows(forRole)
	if asJSON {
		say("%s", jsonIndent(rows))
		return nil
	}
	if len(rows) == 0 {
		if forRole != "" {
			say("no work for %s on this machine", forRole)
			return nil
		}
		say("no work declared on this machine (`fleet dispatch` declares a row)")
		return nil
	}
	now := fleet.Now()
	// Grouped by who is accountable: each hub's rows read as one block, attention
	// first within it; rows with no one accountable come last.
	groups := map[string][]WorkRow{}
	var order []string
	for _, r := range rows {
		acc := fleet.S(r, "for")
		if _, seen := groups[acc]; !seen {
			order = append(order, acc)
		}
		groups[acc] = append(groups[acc], r)
	}
	sort.SliceStable(order, func(i, j int) bool {
		if (order[i] == "") != (order[j] == "") {
			return order[j] == ""
		}
		return order[i] < order[j]
	})
	for _, acc := range order {
		head := acc
		if head == "" {
			head = "no one accountable"
		}
		say("%s (%d)", head, len(groups[acc]))
		for _, r := range groups[acc] {
			say("  %s", WorkLine(r, now))
		}
	}
	scope := "scope: rows declared on this machine; hands from this machine's leases"
	if ages := cacheAges(now); ages != "" {
		scope += "; other machines' rows from the GitHub cache (" + ages + "; `fleet sync` refreshes)"
	}
	say("%s", scope)
	return nil
}

func replaceableDispatch(rid, branch, rel string) (fleet.Rec, error) {
	path := dispatchFile(rid, branch, rel)
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil, nil
	}
	existing := fleet.ReadJSON(path)
	if existing == nil || fleet.S(existing, "repo") != rid || fleet.S(existing, "change") != branch || fleet.S(existing, "relationship") != rel {
		return nil, refuse("fleet dispatch: selected assignment is unreadable or has conflicting identity")
	}
	if fleet.S(existing, "request_id") != "" {
		return nil, refuse("fleet dispatch: this assignment is request-bound; inspect `fleet status` rather than replacing it")
	}
	return existing, nil
}

// scopedDispatchRows preserves unrelated legacy maintenance while refusing every
// damaged target before any mutation. Filename candidates catch unreadable rows;
// decoded identity also catches selected rows stored under an unexpected name.
func scopedDispatchRows(rid, branch, rel string) ([]fleet.Rec, error) {
	entries, err := os.ReadDir(dispatchDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []fleet.Rec
	prefix := fleet.Safe(rid + "__" + branch + "__")
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dispatchDir(), entry.Name())
		candidate := strings.HasPrefix(entry.Name(), prefix)
		if rel != "" {
			candidate = path == dispatchFile(rid, branch, rel)
		}
		row := fleet.ReadJSON(path)
		selected := fleet.S(row, "repo") == rid && fleet.S(row, "change") == branch && (rel == "" || fleet.S(row, "relationship") == rel)
		if !candidate && !selected {
			continue
		}
		if !selected || fleet.S(row, "relationship") == "" || path != dispatchFile(rid, branch, fleet.S(row, "relationship")) {
			return nil, refuse("fleet: selected assignment is unreadable or has conflicting identity")
		}
		rows = append(rows, row)
	}
	return rows, nil
}
