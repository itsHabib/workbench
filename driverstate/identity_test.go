package driverstate

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStateRootPrefersExplicitEnv(t *testing.T) {
	t.Setenv(StateDirEnv, filepath.Join("some", "explicit", "root"))
	dir, source, err := StateRoot()
	if err != nil {
		t.Fatalf("StateRoot: %v", err)
	}
	if dir != filepath.Clean(filepath.Join("some", "explicit", "root")) {
		t.Fatalf("dir = %q, want the explicit env value", dir)
	}
	if !strings.Contains(source, StateDirEnv) {
		t.Fatalf("source = %q, want it to name the env var", source)
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
