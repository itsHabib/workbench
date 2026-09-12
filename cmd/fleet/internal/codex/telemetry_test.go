package codex

import (
	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"testing"
)

func TestRollbackRemovesOnlyUnwoundTakeoverTelemetry(t *testing.T) {
	oldState, oldTrace := fleet.State, fleet.HookTakeovers
	fleet.State = t.TempDir()
	t.Cleanup(func() { fleet.State = oldState; fleet.HookTakeovers = oldTrace })
	key := "r:branch"
	old := fleet.LeaseRecord(key, "old", "", "", nil)
	fresh := fleet.LeaseRecord(key, "new", "", "", nil)
	if err := fleet.WriteLease(key, fresh); err != nil {
		t.Fatal(err)
	}
	fleet.HookTakeovers = []fleet.Rec{{"key": key, "from": "old", "to": "new"}, {"key": "other", "from": "a", "to": "new"}}
	rollbackLeases(map[string]fleet.Rec{key: old}, "new")
	if fleet.S(fleet.Lease(key), "session") != "old" {
		t.Fatal("lease was not restored")
	}
	if len(fleet.HookTakeovers) != 1 || fleet.S(fleet.HookTakeovers[0], "key") != "other" {
		t.Fatal(fleet.HookTakeovers)
	}
}
