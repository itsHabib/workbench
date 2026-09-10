package verbs

import (
	"os"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestRoleHandoffCLIUsesLaunchRoleAcrossBranches(t *testing.T) {
	repo, sid := requestFixture(t)
	if err := os.MkdirAll(fleet.OrgState, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleet.RolesMap(), []byte(repo+" one lead:demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Dispatch([]string{"handoff", "--role", "waiting on the worker", "read the reply", "--session", sid[:8]}); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "checkout", "-b", "next-task")
	v := fleet.Run(fleet.Event{"session_id": "replacement", "cwd": repo, "hook_event_name": "SessionStart"})
	if !strings.Contains(v.Out, "waiting on the worker") || !strings.Contains(v.Out, "read the reply") {
		t.Fatal(v)
	}
	if err := Dispatch([]string{"handoff", "next-task", "branch-specific handoff"}); err != nil {
		t.Fatal("legacy branch syntax failed", err)
	}
	if !strings.Contains(fleet.HandoffLine(fleet.Scope(repo, "next-task"), "next-task"), "branch-specific") {
		t.Fatal("branch handoff not saved")
	}
}

func TestRoleHandoffCLIRejectsMissingConclusion(t *testing.T) {
	requestFixture(t)
	if err := Dispatch([]string{"handoff", "--role"}); err == nil {
		t.Fatal("accepted no conclusion")
	}
}
