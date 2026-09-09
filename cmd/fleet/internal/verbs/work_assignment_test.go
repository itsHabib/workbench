package verbs

import (
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestUndeclaredUsesHolderAssignment(t *testing.T) {
	old := fleet.State
	fleet.State = t.TempDir()
	t.Cleanup(func() { fleet.State = old })
	write := func(p string, r fleet.Rec) {
		t.Helper()
		if err := fleet.WriteJSON(p, r); err != nil {
			t.Fatal(err)
		}
	}
	write(fleet.Path("sessions", "current.json"), fleet.Rec{"session": "current", "slot": "a-current", "last": fleet.Now()})
	write(fleet.KeyFile("leases", "repo:fixture:topic"), fleet.Rec{"key": "repo:fixture:topic", "session": "current", "at": fleet.Now()})
	write(fleet.Path("assign", "a-current.json"), fleet.Rec{"repo": "fixture", "branch": "topic", "slot": "a-current", "delivered_to": "current", "for": "lead:current"})
	write(fleet.Path("assign", "z-old.json"), fleet.Rec{"repo": "fixture", "branch": "topic", "slot": "z-old", "delivered_to": "old", "for": "lead:old"})
	rows := undeclaredRows(map[string]bool{})
	if len(rows) != 1 || fleet.S(rows[0], "for") != "lead:current" || fleet.S(rows[0], "slot") != "a-current" {
		t.Fatalf("wrong assignment attached: %v", rows)
	}
	// A reused seat's record delivered to another session is not the holder's work.
	write(fleet.Path("assign", "a-current.json"), fleet.Rec{"repo": "fixture", "branch": "topic", "slot": "a-current", "delivered_to": "other", "for": "lead:other"})
	rows = undeclaredRows(map[string]bool{})
	if len(rows) != 1 || rows[0]["for"] != nil {
		t.Fatalf("foreign delivery attached: %v", rows)
	}
}
