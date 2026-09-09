package verbs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

var requestID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$`)

// CmdRequest declares an immutable, retry-safe assignment in the existing dispatch
// store. It does not launch, message, accept, acquire a lease, or place a worktree.
// The ID is repo-scoped. Replaying it preserves the original head and timestamp.
func CmdRequest(change, id, worker, lead, brief string) error {
	if fleet.ReadOnly {
		return refuse("fleet request: cannot dispatch in read-only mode")
	}

	if !requestID.MatchString(id) || worker == "" || lead == "" || strings.TrimSpace(brief) == "" {
		return refuse("fleet request: requires --id (1-96 letters/digits/._-), --worker, --for and --brief")
	}
	branch := strings.TrimSpace(change)
	if branch == "" || strings.HasPrefix(branch, "#") {
		return refuse("fleet request: provide a branch name rather than a numbered change")
	}
	rid := fleet.RepoID(cwd())
	if rid == "" {
		return refuse("fleet request: run inside the target repository")
	}
	// Ownership and activity are keyed by the branch as git spells it, never by the
	// caller's spelling: on a case-insensitive filesystem two spellings would
	// otherwise key two assignments for one branch.
	branch = canonicalBranch(cwd(), branch)
	wanted := fleet.Rec{"request_id": id, "repo": rid, "change": branch,
		"worker": worker, "for": lead, "brief": strings.TrimSpace(brief), "relationship": "implementation"}
	replay := false
	err := fleet.KeyLock("dispatch", func() error {
		// Reconcile existing keys like legacy dispatch; a retained collision refuses.
		fleet.MigrateLegacyKeys()
		if fleet.MigrationPending() {
			return refuse("fleet request: legacy ownership needs reconciliation before assigning work")
		}
		rows, err := strictDispatchRows()
		if err != nil {
			return err
		}
		for _, row := range rows {
			if fleet.S(row, "repo") != rid {
				continue
			}
			if fleet.S(row, "request_id") == id {
				if err := validateReplay(row, wanted, worker); err != nil {
					return err
				}
				replay = true
				return nil
			}
		}
		_, _, head, err := resolveDispatchTarget("request", branch)
		if err != nil {
			return err
		}
		sid, err := findSession(worker)
		if err != nil {
			return err
		}
		wanted["worker"] = sid
		return createRequest(rows, wanted, head)
	})
	if err != nil {
		return err
	}
	if replay {
		say("Assignment already recorded; no new dispatch or delivery. Inspect `fleet status` for observed activity.")
		return nil
	}
	say("Queued: %s. Assignment recorded; worker delivery and acceptance are not yet confirmed.", branch)
	return nil
}

// A full retained ID survives session cleanup; prefixes must still resolve
// uniquely. No live branch or worker is required for an identical replay.
func validateReplay(row, wanted fleet.Rec, worker string) error {
	if worker != fleet.S(row, "worker") {
		sid, err := findSession(worker)
		if err != nil {
			return err
		}
		wanted["worker"] = sid
	}
	if !sameRequest(row, wanted) {
		return refuse("fleet request: request ID already has different work or recipient; inspect `fleet status`")
	}
	return nil
}

func sameRequest(a, b fleet.Rec) bool {
	for _, key := range []string{"request_id", "repo", "change", "worker", "for", "brief", "relationship"} {
		if fleet.S(a, key) != fleet.S(b, key) {
			return false
		}
	}
	return true
}

func createRequest(rows []fleet.Rec, wanted fleet.Rec, head string) error {
	rid, branch, sid := fleet.S(wanted, "repo"), fleet.S(wanted, "change"), fleet.S(wanted, "worker")
	for _, row := range rows {
		if fleet.S(row, "repo") == rid && fleet.S(row, "change") == branch {
			return refuse("fleet request: this branch already has an assignment; inspect `fleet status` and existing work before assigning again")
		}
	}
	if !fleet.SessionAlive(fleet.SessionRecord(sid)) {
		return refuse("fleet request: selected worker is not observably live; choose a reachable worker before queuing")
	}
	key := "repo:" + rid + ":" + branch
	// Serialize with the hook's first-write lease acquisition. This check does not
	// reserve the branch: assignment is accountability, not execution authority.
	return fleet.KeyLock(key, func() error {
		if fleet.MigrationPending() {
			return refuse("fleet request: legacy ownership changed during dispatch; inspect state")
		}
		if _, err := os.Lstat(dispatchFile(rid, branch, "implementation")); !os.IsNotExist(err) {
			return refuse("fleet request: assignment path already exists or is unreadable; inspect state")
		}
		if reason := fleet.CheckStop(key, branch, sid); reason != "" {
			return refuse("fleet request: this branch is stopped; no new assignment was recorded")
		}
		lease := fleet.Lease(key)
		if fleet.IsMalformed(lease) {
			return refuse("fleet request: branch ownership is unreadable; inspect it before dispatch")
		}
		if lease != nil && fleet.S(lease, "session") != sid {
			return refuse("fleet request: another session holds this branch; no assignment or takeover performed")
		}
		wanted["at"], wanted["head_at_dispatch"], wanted["by"] = fleet.Now(), head, dispatcher("")
		if err := fleet.WriteJSON(dispatchFile(rid, branch, "implementation"), wanted); err != nil {
			return err
		}
		fleet.ObserveAction("dispatch", wanted)
		return nil
	})
}

// strictDispatchRows refuses gaps instead of letting a damaged row disappear and
// allowing its request ID or branch to be assigned a second time.
func strictDispatchRows() ([]fleet.Rec, error) {
	entries, err := os.ReadDir(dispatchDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var rows []fleet.Rec
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		row := fleet.ReadJSON(filepath.Join(dispatchDir(), entry.Name()))
		if row == nil || fleet.S(row, "repo") == "" || fleet.S(row, "change") == "" || fleet.S(row, "relationship") == "" {
			return nil, fmt.Errorf("assignment evidence unreadable: %s", entry.Name())
		}
		// A request-bound row carries who it went to and when; without either it is
		// damaged evidence, and damaged evidence must not read as a complete status.
		if fleet.S(row, "request_id") != "" && (fleet.S(row, "worker") == "" || fleet.S(row, "for") == "" || fleet.F(row, "at") <= 0) {
			return nil, fmt.Errorf("assignment evidence damaged: %s", entry.Name())
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// canonicalBranch is the branch as git lists it. An exact match wins; else a
// unique case-insensitive match is the same ref spelled differently; else the
// caller's spelling stands (a branch that does not exist yet keeps its name).
func canonicalBranch(dir, cand string) string {
	rc, out := gitTry(dir, gitTimeout, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if rc != 0 {
		return cand
	}
	match := ""
	for _, name := range strings.Split(out, "\n") {
		name = strings.TrimSpace(name)
		if name == cand {
			return cand
		}
		if name != "" && strings.EqualFold(name, cand) {
			if match != "" {
				return cand // two refs differ only by case: git itself is ambiguous here
			}
			match = name
		}
	}
	if match != "" {
		return match
	}
	return cand
}

func dispatchRequest(args []string) error {
	vals := map[string]string{}
	for _, flag := range []string{"--id", "--worker", "--for", "--brief"} {
		value, err := optValue(args, flag, "request")
		if err != nil {
			return err
		}
		vals[flag] = value
	}
	pos := positional(args, "--id", "--worker", "--for", "--brief")
	if len(pos) != 1 {
		return refuse("usage: fleet request <branch> --id <request> --worker <session> --for <lead> --brief <text>")
	}
	return CmdRequest(pos[0], vals["--id"], vals["--worker"], vals["--for"], vals["--brief"])
}
