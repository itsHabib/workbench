package verbs

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestObservationalDispatchNeverMigrates(t *testing.T) {
	oldState, oldOrg, oldOut, oldReadOnly := fleet.State, fleet.OrgState, Out, fleet.ReadOnly
	root := t.TempDir()
	fleet.State, fleet.OrgState, Out = filepath.Join(root, "fleet"), filepath.Join(root, "org"), io.Discard
	t.Cleanup(func() { fleet.State, fleet.OrgState, Out, fleet.ReadOnly = oldState, oldOrg, oldOut, oldReadOnly })
	for _, verb := range []string{"work", "receipts", "decisions", "done"} {
		_ = Dispatch([]string{verb, "--json"})
		if _, err := os.Stat(fleet.State); !os.IsNotExist(err) {
			t.Fatalf("%s initialized state: %v", verb, err)
		}
	}
	legacy := filepath.Join(fleet.State, "leases", "old.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"repo":"sample","branch":"main","session":"dead"}`)
	if err := os.WriteFile(legacy, body, 0600); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"work", "receipts", "decisions", "done"} {
		if err := Dispatch([]string{verb, "--json"}); err == nil {
			t.Fatalf("%s ignored legacy ambiguity", verb)
		}
	}
	got, err := os.ReadFile(legacy)
	if err != nil || string(got) != string(body) {
		t.Fatalf("legacy changed: %s %v", got, err)
	}
	entries, err := os.ReadDir(fleet.State)
	if err != nil || len(entries) != 1 || entries[0].Name() != "leases" {
		t.Fatalf("state changed: %v %v", entries, err)
	}
	if fleet.ReadOnly != oldReadOnly {
		t.Fatal("read-only mode leaked")
	}
}
