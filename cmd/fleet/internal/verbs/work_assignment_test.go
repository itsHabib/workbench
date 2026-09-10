package verbs

import (
	"os"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestUndeclaredUsesCurrentPlacementAcrossReplacement(t *testing.T) {
	repo, sid := requestFixture(t)
	if err := os.MkdirAll(fleet.OrgState, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleet.RolesMap(), []byte(repo+" one worker:demo a-current\n"), 0600); err != nil {
		t.Fatal(err)
	}
	write := func(p string, r fleet.Rec) {
		t.Helper()
		if err := fleet.WriteJSON(p, r); err != nil {
			t.Fatal(err)
		}
	}
	rec := fleet.SessionRecord(sid)
	rec["slot"], rec["launch_dir"] = "a-current", repo
	write(fleet.Path("sessions", sid+".json"), rec)
	rid, key := fleet.RepoID(repo), fleet.Scope(repo, "task")
	write(fleet.KeyFile("leases", key), fleet.Rec{"key": key, "session": sid, "at": fleet.Now()})
	a := fleet.Rec{"repo": rid, "branch": "task", "slot": "a-current", "path": repo,
		"delivered_to": "predecessor", "for": "lead:current", "at": fleet.F(rec, "last_event_at") - 1, "role": "worker:demo", "tenant": "one"}
	path := fleet.Path("assign", "a-current.json")
	write(path, a)
	write(fleet.Path("assign", "z-old.json"), fleet.Rec{"repo": rid, "branch": "task", "slot": "z-old", "delivered_to": "old", "for": "lead:old"})
	before := readBytes(t, path)
	rows := undeclaredRows(map[string]bool{})
	if len(rows) != 1 || fleet.S(rows[0], "for") != "lead:current" || fleet.S(rows[0], "slot") != "a-current" {
		t.Fatalf("replacement lost current placement: %v", rows)
	}
	if string(readBytes(t, path)) != string(before) {
		t.Fatal("board changed assignment while reading")
	}
	// A reused name must not associate another path, repository or branch.
	for _, field := range []string{"repo", "branch", "slot", "path"} {
		value := a[field]
		a[field] = "other"
		write(path, a)
		assertNoHolderAssignment(t)
		a[field] = value
	}
	write(path, a)
	// Current filesystem state also matters when persisted session facts are old.
	runGit(t, repo, "checkout", "-b", "reused-branch")
	assertNoHolderAssignment(t)
	runGit(t, repo, "checkout", "task")
	// A later placement on the same branch cannot rename a predecessor's owner.
	a["at"] = fleet.F(rec, "last_event_at") + 1
	write(path, a)
	assertNoHolderAssignment(t)
	a["at"] = fleet.F(rec, "last_event_at") - 1
	write(path, a)
	for _, binding := range []string{"two worker:demo", "one worker:other"} {
		if err := os.WriteFile(fleet.RolesMap(), []byte(repo+" "+binding+" a-current\n"), 0600); err != nil {
			t.Fatal(err)
		}
		assertNoHolderAssignment(t)
	}
	if err := os.WriteFile(fleet.RolesMap(), []byte(repo+" one worker:demo a-current\n"), 0600); err != nil {
		t.Fatal(err)
	}
	delete(a, "tenant")
	write(path, a)
	assertNoHolderAssignment(t)
	if err := os.WriteFile(fleet.RolesMap(), []byte(repo+" one worker:demo new-seat\n"), 0600); err != nil {
		t.Fatal(err)
	}
	assertNoHolderAssignment(t)
}

func assertNoHolderAssignment(t *testing.T) {
	t.Helper()
	rows := undeclaredRows(map[string]bool{})
	if len(rows) != 1 || rows[0]["for"] != nil {
		t.Fatalf("stale placement attached to holder: %v", rows)
	}
}

func TestAssignCapturesDestinationIdentity(t *testing.T) {
	repo, _ := requestFixture(t)
	seat := t.TempDir()
	runGit(t, repo, "worktree", "add", "--detach", seat)
	runGit(t, repo, "branch", "seat-task")
	if err := os.MkdirAll(fleet.OrgState, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleet.RolesMap(), []byte(seat+" one worker:demo seat-a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CmdAssign("seat-a", "seat-task", "the current brief", "lead:demo", "lead:demo", ""); err != nil {
		t.Fatal(err)
	}
	a := fleet.ReadJSON(fleet.Path("assign", "seat-a.json"))
	if fleet.S(a, "tenant") != "one" || fleet.S(a, "role") != "worker:demo" || fleet.S(a, "for") != "lead:demo" {
		t.Fatal("destination identity not captured independently of accountable lead", a)
	}
	v := fleet.Run(fleet.Event{"hook_event_name": "SessionStart", "session_id": "first-worker", "cwd": seat})
	if !strings.Contains(v.Out, "the current brief") {
		t.Fatal("new assignment needed additional setup", v.Out)
	}
}
