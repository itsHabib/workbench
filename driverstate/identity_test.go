package driverstate

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStateRootPrefersExplicitEnv(t *testing.T) {
	root := t.TempDir() // already absolute
	t.Setenv(StateDirEnv, root)
	dir, source, err := StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}
	if dir != filepath.Clean(root) {
		t.Fatalf("dir = %q, want the explicit env value %q", dir, root)
	}
	if !strings.Contains(source, StateDirEnv) {
		t.Fatalf("source = %q, want it to name the env var", source)
	}
}

// A relative WORKBENCH_STATE_DIR is REJECTED: filepath.Abs would only re-anchor
// it to each process's cwd, so a CLI in a subdir and an MCP server at the repo
// root would still split roots. Absolute-or-error is the only safe rule (spec §6 P2).
func TestStateRootRejectsRelativeEnv(t *testing.T) {
	t.Setenv(StateDirEnv, filepath.Join("some", "rel", "root"))
	_, _, err := StateRoot()
	if err == nil {
		t.Fatal("StateRoot should reject a relative WORKBENCH_STATE_DIR")
	}
	if !strings.Contains(err.Error(), "must be absolute") {
		t.Fatalf("error = %v, want it to say the override must be absolute", err)
	}
}

func TestStateRootFallsBackToUserProfile(t *testing.T) {
	t.Setenv(StateDirEnv, "")
	dir, source, err := StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}
	if !strings.HasSuffix(dir, filepath.Join(".workbench", "driver-state")) {
		t.Fatalf("dir = %q, want it under the user profile .workbench/driver-state", dir)
	}
	if source != "user profile" {
		t.Fatalf("source = %q, want \"user profile\"", source)
	}
}

func TestMintedIDsCarryTheirPrefix(t *testing.T) {
	ev, err := NewEventID()
	if err != nil || !strings.HasPrefix(ev, "evt_") {
		t.Fatalf("NewEventID = %q, %v", ev, err)
	}
	run, err := NewRunID()
	if err != nil || !strings.HasPrefix(run, "dsr_") {
		t.Fatalf("NewRunID = %q, %v", run, err)
	}
	if ev == run {
		t.Fatal("minted ids collided")
	}
}
