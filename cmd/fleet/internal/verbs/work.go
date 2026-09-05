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
func CmdDispatch(change, rel, forRole, due, slot, brief, by string, take bool) error {
	usage := `usage: fleet dispatch <branch|#n> --as <relationship> [--for <role>] [--due 45m] [--slot <name>] [--brief "<one line>"] [--take]`
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
	if by == "" {
		by = "operator"
	}
	if forRole == "" {
		forRole = by
	}
	key := "repo:" + rid + ":" + branch
	existing := fleet.ReadJSON(dispatchFile(rid, branch, rel))
	if hands := liveHands(key); hands != "" && existing != nil && !take {
		return refuse("fleet dispatch: %s/%s already has live hands (%s, for %s); `--take` rewrites the row, or `fleet work` to see it",
			branch, rel, fleet.Short(hands), fleet.S(existing, "for"))
	}
	if slot != "" {
		if err := CmdAssign(slot, branch, brief, by, forRole); err != nil {
			return err
		}
	}
	now := fleet.Now()
	rec := fleet.Rec{"change": branch, "repo": rid, "relationship": rel, "for": forRole, "by": by, "at": now,
		"due": nil, "slot": nilIfEmpty(slot), "brief": nilIfEmpty(strings.TrimSpace(brief)), "head_at_dispatch": nilIfEmpty(sha)}
	if dueSecs > 0 {
		rec["due"] = now + dueSecs
	}
	if err := fleet.WriteJSON(dispatchFile(rid, branch, rel), rec); err != nil {
		return err
	}
	tail := ""
	if dueSecs > 0 {
		tail += ", due in " + fleet.FmtAge(dueSecs)
	}
	if slot != "" {
		tail += ", in " + slot
	}
	say("dispatched %s/%s for %s%s", branch, rel, forRole, tail)
	return nil
}

// CmdReassign moves every row of a change to another accountable role. Two
// commands split a hub: bind the second role's directory, then reassign its rows.
func CmdReassign(change, forRole string) error {
	if change == "" || forRole == "" {
		return refuse("usage: fleet reassign <branch|#n> --for <role>")
	}
	rid, branch, _, err := resolveDispatchTarget("reassign", change)
	if err != nil {
		return err
	}
	n := 0
	for _, r := range dispatchRows() {
		if fleet.S(r, "repo") != rid || fleet.S(r, "change") != branch {
			continue
		}
		r["for"] = forRole
		r["reassigned_at"] = fleet.Now()
		if err := fleet.WriteJSON(dispatchFile(rid, branch, fleet.S(r, "relationship")), r); err != nil {
			return err
		}
		n++
	}
	if n == 0 {
		return refuse("fleet reassign: no row for %s here; `fleet dispatch` declares one", branch)
	}
	say("%s: %d row(s) now for %s", branch, n, forRole)
	return nil
}

// cmdUndispatch retires a change's rows (one relationship, or all of them).
func cmdUndispatch(change, rel string) error {
	if change == "" {
		return refuse("usage: fleet undispatch <branch|#n> [--as <relationship>]")
	}
	rid, branch, _, err := resolveDispatchTarget("undispatch", change)
	if err != nil {
		return err
	}
	n := 0
	for _, r := range dispatchRows() {
		if fleet.S(r, "repo") != rid || fleet.S(r, "change") != branch || (rel != "" && fleet.S(r, "relationship") != rel) {
			continue
		}
		fleet.Unlink(dispatchFile(rid, branch, fleet.S(r, "relationship")))
		n++
	}
	if n == 0 {
		return refuse("fleet undispatch: no row for %s here", branch)
	}
	say("%s: %d row(s) retired", branch, n)
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
	var rows []WorkRow
	for _, d := range dispatchRows() {
		rid, branch, rel := fleet.S(d, "repo"), fleet.S(d, "change"), fleet.S(d, "relationship")
		key := "repo:" + rid + ":" + branch
		declared[key] = true
		row := WorkRow{"change": branch, "repo": rid, "relationship": rel, "for": d["for"], "by": d["by"], "at": d["at"],
			"due": d["due"], "slot": d["slot"], "brief": d["brief"], "key": key, "hands": nil, "state": "dispatched", "head": nil, "done_at": nil}
		state := "dispatched"
		if cur := fleet.Lease(key); cur != nil && !fleet.IsMalformed(cur) && !fleet.B(cur, "occupancy") {
			sid := fleet.S(cur, "session")
			row["hands"] = sid
			srec := fleet.SessionRecord(sid)
			switch {
			case !fleet.SessionAlive(srec):
				state = "dead"
			case fleet.B(srec, "turn_open"):
				state = "working"
			default:
				state = "idle"
			}
		}
		if head := branchHead(rid, branch); head != "" {
			row["head"] = head
			for _, r := range receiptRows(head, rel, 0, false) {
				if fleet.S(r, "malformed") != "" {
					continue
				}
				if fleet.S(r, "verdict") == "pass" {
					state = "done"
					row["done_at"] = r["at"]
				}
				break // rows are newest first: the latest receipt of the kind decides
			}
		}
		if due := fleet.F(d, "due"); due > 0 && now > due && state != "done" && state != "dead" {
			state = "late"
		}
		row["state"] = state
		rows = append(rows, row)
	}
	for _, l := range leaseRows() {
		key := fleet.S(l, "key")
		if fleet.B(l, "occupancy") || fleet.IsResource(key) || declared[key] {
			continue
		}
		sid := fleet.S(l, "session")
		if !fleet.SessionAlive(fleet.SessionRecord(sid)) {
			continue
		}
		parts := fleet.KeyParts(key)
		rows = append(rows, WorkRow{"change": fleet.S(parts, "branch"), "repo": fleet.S(parts, "repo"), "relationship": nil, "for": nil, "by": nil,
			"at": l["at"], "due": nil, "slot": nil, "brief": nil, "key": key, "hands": sid, "state": "undeclared", "head": nil, "done_at": nil})
	}
	if forRole != "" {
		var mine []WorkRow
		for _, r := range rows {
			if fleet.S(r, "for") == forRole {
				mine = append(mine, r)
			}
		}
		rows = mine
	}
	order := map[string]int{"dead": 0, "late": 1, "undeclared": 2, "working": 3, "idle": 4, "dispatched": 5, "done": 6}
	sortBy(rows, func(a, b WorkRow) bool {
		oa, ob := order[fleet.S(a, "state")], order[fleet.S(b, "state")]
		if oa != ob {
			return oa < ob
		}
		return fleet.S(a, "change") < fleet.S(b, "change")
	})
	return rows
}

// WorkAttention is the set of work states a hub must decide something about.
var WorkAttention = map[string]bool{"dead": true, "late": true, "undeclared": true}

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
	for _, r := range rows {
		say("%s", WorkLine(r, now))
	}
	say("scope: rows declared on this machine; hands from this machine's leases")
	return nil
}
