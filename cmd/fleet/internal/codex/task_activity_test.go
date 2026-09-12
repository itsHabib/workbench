package codex

import (
	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"os"
	"path/filepath"
	"testing"
)

func TestPatchAdapterEnforcesLeaseAndRecordsActivity(t *testing.T) {
	oldState, oldOrg := fleet.State, fleet.OrgState
	root := t.TempDir()
	fleet.State, fleet.OrgState = filepath.Join(root, "state"), filepath.Join(root, "org")
	t.Cleanup(func() { fleet.State, fleet.OrgState = oldState, oldOrg })
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git", "refs", "heads"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/task\n"), 0600); err != nil {
		t.Fatal(err)
	}
	key := fleet.Scope(repo, "task")
	if err := fleet.WriteJSON(fleet.Path("sessions", "holder.json"), fleet.Rec{"session": "holder", "pid": os.Getpid(), "pid_kind": "harness", "last_event_at": fleet.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := fleet.WriteLease(key, fleet.LeaseRecord(key, "holder", "", repo, nil)); err != nil {
		t.Fatal(err)
	}
	ev := fleet.Event{"session_id": "worker", "cwd": repo, "hook_event_name": "PreToolUse", "tool_name": "apply_patch", "tool_input": fleet.Rec{"command": "*** Begin Patch\n*** Add File: file.txt\n+hello\n*** End Patch"}}
	if v := Run(ev); v.Code != 2 {
		t.Fatalf("patch escaped foreign lease: %v", v)
	}
	if fleet.S(fleet.Lease(key), "session") != "holder" {
		t.Fatal("foreign lease changed")
	}
	ev["session_id"] = "holder"
	if v := Run(ev); v.Code != 0 {
		t.Fatal(v)
	}
	ev["hook_event_name"] = "PostToolUse"
	if v := Run(ev); v.Code != 0 {
		t.Fatal(v)
	}
	write := fleet.M(fleet.M(fleet.SessionRecord("holder"), "last_writes"), key)
	if fleet.S(write, "key") != key || fleet.F(write, "at") == 0 {
		t.Fatalf("patch observation absent: %v", write)
	}
}
