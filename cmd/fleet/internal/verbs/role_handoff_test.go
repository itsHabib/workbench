package verbs

import (
	"errors"
	"os"
	"os/exec"
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
	var refusal *Refusal
	if err := Dispatch([]string{"handoff", "--role"}); !errors.As(err, &refusal) || refusal.Code != 2 {
		t.Fatal("missing conclusion was not usage", err)
	}
}

func TestRoleHandoffExitCodes(t *testing.T) {
	repo, sid := requestFixture(t)
	if err := os.MkdirAll(fleet.OrgState, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		binding string
		code    int
	}{
		{"pooled", "one worker:demo seat-a", 1},
		{"unidentified", "", 1},
		{"blank", "one lead:demo", 2},
		{"oversize", "one lead:demo", 2},
		{"utf8", "one lead:demo", 2},
		{"io", "one lead:demo", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(fleet.RolesMap(), []byte(repo+" "+tc.binding+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.name == "io" {
				if err := os.WriteFile(fleet.Path("role-handoff"), []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestRoleHandoffExitHelper$")
			cmd.Dir = repo
			cmd.Env = append(os.Environ(), "FLEET_HANDOFF_EXIT_HELPER=1", "FLEET_HANDOFF_CASE="+tc.name,
				"FLEET_STATE="+fleet.State, "ORG_STATE="+fleet.OrgState, "FLEET_HANDOFF_SESSION="+sid)
			out, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != tc.code {
				t.Fatalf("wanted exit %d, got %v: %s", tc.code, err, out)
			}
		})
	}
}

func TestRoleHandoffExitHelper(_ *testing.T) {
	if os.Getenv("FLEET_HANDOFF_EXIT_HELPER") != "1" {
		return
	}
	body := "useful conclusion"
	switch os.Getenv("FLEET_HANDOFF_CASE") {
	case "blank":
		body = " "
	case "oversize":
		body = strings.Repeat("x", 16385)
	case "utf8":
		body = string([]byte{0xff})
	}
	Run([]string{"handoff", "--role", body, "--session", os.Getenv("FLEET_HANDOFF_SESSION")})
}
