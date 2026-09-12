package verbs

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestObservationSnapshotDoesNotRefreshSlotIndex(t *testing.T) {
	oldState, oldOrg, oldOut := fleet.State, fleet.OrgState, Out
	root := t.TempDir()
	fleet.State, fleet.OrgState = filepath.Join(root, "fleet"), filepath.Join(root, "org")
	Out = io.Discard
	t.Cleanup(func() { fleet.State, fleet.OrgState, Out = oldState, oldOrg, oldOut })
	repo := filepath.Join(root, "seat")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	run("init")
	file := filepath.Join(repo, "tracked")
	if err := os.WriteFile(file, []byte("same content"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "tracked")
	index := filepath.Join(repo, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fleet.OrgState, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleet.RolesMap(), []byte(repo+" test finisher:fixture fixture-1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Dispatch([]string{"report", "--snapshot"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(index)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("snapshot refreshed index: %v", err)
	}
	run("status", "--porcelain") // positive control: ordinary status refreshes stat data
	refreshed, err := os.ReadFile(index)
	if err != nil || bytes.Equal(before, refreshed) {
		t.Fatalf("fixture did not exercise an index refresh: %v", err)
	}
}

func TestObservationSnapshotDoesNotMigrateOrInitialize(t *testing.T) {
	oldState, oldOrg, oldOut, oldReadOnly := fleet.State, fleet.OrgState, Out, fleet.ReadOnly
	root := t.TempDir()
	fleet.State, fleet.OrgState = filepath.Join(root, "fleet"), filepath.Join(root, "org")
	var output bytes.Buffer
	Out = &output
	t.Cleanup(func() { fleet.State, fleet.OrgState, Out, fleet.ReadOnly = oldState, oldOrg, oldOut, oldReadOnly })
	if err := Dispatch([]string{"report", "--snapshot"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fleet.State); !os.IsNotExist(err) {
		t.Fatalf("snapshot initialized state: %v", err)
	}
	legacy := filepath.Join(fleet.State, "leases", "legacy.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"repo":"example","branch":"main","session":"worker"}`)
	if err := os.WriteFile(legacy, want, 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := Dispatch([]string{"report", "--snapshot"}); err != nil {
		t.Fatal(err)
	}
	var packet map[string]any
	if err := json.Unmarshal(output.Bytes(), &packet); err != nil {
		t.Fatal(err)
	}
	if packet["schema"] != "fleet-observation-v1" || packet["migration_pending"] != true {
		t.Fatalf("snapshot omitted migration evidence: %v", packet)
	}
	got, err := os.ReadFile(legacy)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("snapshot modified legacy lease: %s %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(fleet.State, "migrated-keys.v1")); !os.IsNotExist(err) {
		t.Fatalf("snapshot ran migration: %v", err)
	}
	if fleet.ReadOnly != oldReadOnly {
		t.Fatal("snapshot leaked read-only mode into later verbs")
	}
}

func TestObservationSnapshotReportsCompletedMigrationCollision(t *testing.T) {
	oldState, oldOrg, oldOut := fleet.State, fleet.OrgState, Out
	root := t.TempDir()
	fleet.State, fleet.OrgState = filepath.Join(root, "fleet"), filepath.Join(root, "org")
	var output bytes.Buffer
	Out = &output
	t.Cleanup(func() { fleet.State, fleet.OrgState, Out = oldState, oldOrg, oldOut })
	key := "repo:example:main"
	legacy := fleet.Path("leases", "legacy.json")
	want := []byte(`{"repo":"example","branch":"main","session":"old-holder"}`)
	fleet.WriteJSON(fleet.KeyFile("leases", key), fleet.Rec{"key": key, "session": "new-holder"})
	if err := os.WriteFile(legacy, want, 0o600); err != nil {
		t.Fatal(err)
	}
	fleet.MigrateLegacyKeys()
	marker := fleet.Path("migrated-keys.v1")
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	// Make the marker unequivocally newer than the directory: only the retained
	// collision, not timestamp movement, can explain pending reconciliation.
	later := time.Now().Add(time.Second)
	if err := os.Chtimes(marker, later, later); err != nil {
		t.Fatal(err)
	}
	if err := Dispatch([]string{"report", "--snapshot"}); err != nil {
		t.Fatal(err)
	}
	var packet map[string]any
	if err := json.Unmarshal(output.Bytes(), &packet); err != nil {
		t.Fatal(err)
	}
	if packet["migration_pending"] != true {
		t.Fatalf("completed migration concealed collision: %v", packet)
	}
	got, err := os.ReadFile(legacy)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("snapshot modified collision: %s %v", got, err)
	}
	after, err := os.ReadFile(marker)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("snapshot changed migration marker: %v", err)
	}
}

func TestReportBypassesMigrationAndRejectsInvalidWindow(t *testing.T) {
	oldState, oldOut := fleet.State, Out
	fleet.State = filepath.Join(t.TempDir(), "absent")
	Out = io.Discard
	t.Cleanup(func() { fleet.State = oldState; Out = oldOut })
	if err := Dispatch([]string{"report"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fleet.State); !os.IsNotExist(err) {
		t.Fatalf("report created state: %v", err)
	}
	for _, args := range [][]string{{"report", "--since"}, {"report", "--since", "0s"}, {"report", "--since", "-1h"}, {"report", "--json"}} {
		if Dispatch(args) == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if err := Dispatch([]string{"report", "--since", "48h"}); err != nil {
		t.Fatal(err)
	}
}
