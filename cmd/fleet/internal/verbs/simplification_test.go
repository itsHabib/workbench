package verbs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func crossRepoSeat(t *testing.T) (string, string, string) {
	t.Helper()
	lead, _ := requestFixture(t)
	repo := t.TempDir()
	runGit(t, repo, "init", "-b", "main")
	runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	runGit(t, repo, "remote", "add", "origin", "https://github.com/acme/second.git")
	runGit(t, repo, "branch", "work")
	seat := t.TempDir()
	runGit(t, repo, "worktree", "add", "--detach", seat)
	if err := os.MkdirAll(fleet.OrgState, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleet.RolesMap(), []byte(fmt.Sprintf("%s one supervisor:lead desktop-lead\n%s one author:second worker-seat\n", lead, seat)), 0600); err != nil {
		t.Fatal(err)
	}
	// No fetch is needed for these local branches; keep tests independent of GitHub.
	runGit(t, repo, "remote", "set-url", "origin", repo)
	return lead, repo, seat
}

func TestDispatchAcrossRepositoriesKeepsCallerAndReplyAddress(t *testing.T) {
	lead, repo, seat := crossRepoSeat(t)
	if err := CmdDispatch("work", "implementation", "", "", "worker-seat", "Ship the second repo", "", "", false); err != nil {
		t.Fatal(err)
	}
	if fleet.CanonPath(cwd()) != fleet.CanonPath(lead) || fleet.BranchOf(seat) != "work" {
		t.Fatalf("caller %s (want %s), branch %s", cwd(), lead, fleet.BranchOf(seat))
	}
	r := fleet.ReadJSON(dispatchFile(fleet.RepoID(repo), "work", "implementation"))
	if fleet.S(r, "by") != "supervisor:lead" || fleet.S(r, "for") != "supervisor:lead" {
		t.Fatal("caller identity changed", r)
	}
	a := fleet.ReadJSON(fleet.Path("assign", "worker-seat.json"))
	if fleet.S(a, "reply_to") != "desktop-lead" {
		t.Fatal("worker was handed a role kind or session ID instead of its lead's mailbox", a)
	}
}

func TestExplicitRepoRejectsSeatFromAnotherRepository(t *testing.T) {
	lead, _, seat := crossRepoSeat(t)
	if err := CmdDispatch("task", "implementation", "lead", "", "worker-seat", "wrong target", "", "", false, lead); err == nil {
		t.Fatal("assigned a seat from a different repository")
	}
	if fleet.BranchOf(seat) != "" || len(dispatchRows()) != 0 {
		t.Fatal("refusal changed the seat or published work")
	}
}

func TestDirtySameBranchResumeAndUnassignPreserveFiles(t *testing.T) {
	_, repo, seat := crossRepoSeat(t)
	if err := CmdDispatch("work", "implementation", "lead", "", "worker-seat", "do work", "", "", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(seat, ".jira-fetch.js")
	want := []byte("unfinished scratch and work\n")
	if err := os.WriteFile(path, want, 0600); err != nil {
		t.Fatal(err)
	}
	if err := CmdAssign("worker-seat", "work", "resume existing work", "", "lead", ""); err != nil {
		t.Fatalf("scratch file blocked continuation: %v", err)
	}
	if err := CmdAssign("worker-seat", "main", "different work", "", "lead", ""); err == nil {
		t.Fatal("dirty worktree was repurposed")
	}
	if err := cmdUnassign("worker-seat"); err != nil {
		t.Fatal(err)
	}
	if fleet.ReadJSON(dispatchFile(fleet.RepoID(repo), "work", "implementation")) != nil || fleet.ReadJSON(fleet.Path("assign", "worker-seat.json")) != nil {
		t.Fatal("unassign left a second assignment behind")
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("work was lost: %q %v", got, err)
	}
}

func TestLiveWorkerCannotBeReassignedAndUnassignKeepsItsSession(t *testing.T) {
	_, _, seat := crossRepoSeat(t)
	if err := CmdAssign("worker-seat", "work", "do work", "", "lead", ""); err != nil {
		t.Fatal(err)
	}
	if err := fleet.WriteJSON(fleet.Path("sessions", "worker.json"), fleet.Rec{"session": "worker", "cwd": seat, "branch": "work", "last_event_at": fleet.Now(), "pid_kind": "harness", "pid": os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if err := CmdAssign("worker-seat", "work", "resume", "", "lead", ""); err == nil {
		t.Fatal("reassigned a live worker")
	}
	if err := cmdUnassign("worker-seat"); err != nil {
		t.Fatal(err)
	}
	if fleet.SessionRecord("worker") == nil {
		t.Fatal("clearing the declaration removed the live session")
	}
}

func TestNewMailGeneratesIDAndReturnedIDRemainsRetrySafe(t *testing.T) {
	repo, sid := requestFixture(t)
	peer := t.TempDir()
	if err := os.MkdirAll(fleet.OrgState, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleet.RolesMap(), []byte(fmt.Sprintf("%s t hub:a\n%s t hub:b\n", repo, peer)), 0600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	Out = &out
	if err := CmdSend("hub:b", "", "question", "unit?", "", "ms or s", sid); err != nil {
		t.Fatal(err)
	}
	var sent fleet.Rec
	if err := json.Unmarshal([]byte(out.String()), &sent); err != nil {
		t.Fatal(err)
	}
	id := fleet.S(sent, "id")
	if !strings.HasPrefix(id, "m-") {
		t.Fatal("missing generated id", sent)
	}
	before, _ := os.ReadFile(fleet.MailPath("hub:b", id))
	if err := CmdSend("hub:b", id, "question", "unit?", "", "ms or s", sid); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(fleet.MailPath("hub:b", id))
	if !bytes.Equal(before, after) {
		t.Fatal("retry changed the message")
	}
	if err := CmdSend("hub:b", id, "answer", "unit?", "", "milliseconds", sid); err == nil {
		t.Fatal("changed message overwrote an existing ID")
	}
}
