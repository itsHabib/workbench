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

// A relative WORKBENCH_STATE_DIR must resolve to an ABSOLUTE path so a CLI in a
// subdir and an MCP server at the repo root can't split roots (spec §6 P2).
func TestStateRootResolvesRelativeEnvToAbsolute(t *testing.T) {
	rel := filepath.Join("some", "rel", "root")
	t.Setenv(StateDirEnv, rel)
	dir, _, err := StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}
	if !filepath.IsAbs(dir) {
		t.Fatalf("dir = %q, want an absolute path", dir)
	}
	if !strings.HasSuffix(dir, filepath.Clean(rel)) {
		t.Fatalf("dir = %q, want it to end with the resolved relative path %q", dir, filepath.Clean(rel))
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
