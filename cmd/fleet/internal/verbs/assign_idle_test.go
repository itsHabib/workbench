package verbs

import (
	"os"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// Assignment follows the lease rule: a branch whose holder is active on it is refused,
// one whose holder has gone quiet is not — the seat's first write takes it over.
func TestAssignRefusesOnlyAnActiveHolder(t *testing.T) {
	_, seat, _ := poolFixture(t)
	oldState, oldIdle := fleet.State, fleet.IdleS
	fleet.State, fleet.IdleS = t.TempDir(), 1800
	t.Cleanup(func() { fleet.State, fleet.IdleS = oldState, oldIdle })
	poolMap(t, seat+" work worker:mono seat-a")
	key := fleet.Scope(seat, "feat/x")
	hold := func(quiet float64) {
		t.Helper()
		rec := fleet.Rec{"session": "h0000001", "pid": float64(os.Getpid()), "pid_kind": "harness", "last_event_at": fleet.Now() - quiet, "turn_open": true}
		if err := fleet.WriteJSON(fleet.Path("sessions", "h0000001.json"), rec); err != nil {
			t.Fatal(err)
		}
		lease := fleet.LeaseRecord(key, "h0000001", "rooms", "", "claimed on first write")
		lease["since"] = fleet.Now() - 3*3600
		if err := fleet.WriteLease(key, lease); err != nil {
			t.Fatal(err)
		}
	}
	hold(60)
	if err := assignGuards("seat-a", seat, "feat/x"); err == nil || !strings.Contains(err.Error(), "active on it") {
		t.Fatalf("an active holder's branch must be refused: %v", err)
	}
	hold(4 * 3600)
	if err := assignGuards("seat-a", seat, "feat/x"); err != nil {
		t.Fatalf("a quiet holder must not block an assignment: %v", err)
	}
}
