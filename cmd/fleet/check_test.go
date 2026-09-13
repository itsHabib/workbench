package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestCheckExitCodes(t *testing.T) {
	if os.Getenv("FLEET_TEST_CHECK_CHILD") == "1" {
		runCheck(os.Args[3:])
		return
	}
	root := t.TempDir()
	state, org := filepath.Join(root, "fleet"), filepath.Join(root, "org")
	for _, dir := range []string{state, org} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	config := fleet.Rec{"check-probe": fleet.Rec{"cwd": root, "provider": "codex"}}
	if err := fleet.WriteJSON(filepath.Join(state, "deliver.json"), config); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(org, "roles.map"), []byte(root+" check-tenant check-probe\n"), 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		code int
	}{{nil, 2}, {[]string{"missing-address"}, 1}, {[]string{"check-probe"}, 4}} {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		cmd := exec.CommandContext(ctx, exe, append([]string{"-test.run=^TestCheckExitCodes$", "--"}, tc.args...)...)
		// A missing Node executable is an operational failure, not a refusal.
		cmd.Env = append(os.Environ(), "FLEET_TEST_CHECK_CHILD=1", "FLEET_STATE="+state, "ORG_STATE="+org, "ORG_TENANT=check-tenant", "PATH=")
		out, err := cmd.CombinedOutput()
		cancel()
		if err == nil || cmd.ProcessState.ExitCode() != tc.code {
			t.Fatalf("args %v: wanted %d: %v %s", tc.args, tc.code, err, out)
		}
	}
}
