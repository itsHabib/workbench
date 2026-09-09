package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"github.com/itsHabib/workbench/filelock"
)

func TestLockedWatcherHeartbeatEvidence(t *testing.T) {
	old := fleet.State
	fleet.State = t.TempDir()
	t.Cleanup(func() { fleet.State = old })
	if err := os.MkdirAll(dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	owner, err := os.OpenFile(filepath.Join(dir(), "owner.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := filelock.TryLock(owner); err != nil {
		t.Fatal(err)
	}
	defer filelock.Unlock(owner)
	for _, tc := range []struct {
		name      string
		heartbeat fleet.Rec
		want      string
	}{
		{"absent", nil, "heartbeat unknown"},
		{"empty", fleet.Rec{}, "heartbeat unknown"},
		{"missing time", fleet.Rec{"pid": 123}, "heartbeat unknown"},
		{"missing pid", fleet.Rec{"at": fleet.Now()}, "heartbeat unknown"},
		{"recorded", fleet.Rec{"pid": 123, "at": fleet.Now() - 60}, "last recorded heartbeat pid 123"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Remove(filepath.Join(dir(), "heartbeat.json")); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if tc.heartbeat != nil {
				if err := fleet.WriteJSON(filepath.Join(dir(), "heartbeat.json"), tc.heartbeat); err != nil {
					t.Fatal(err)
				}
			}
			err := Serve(time.Minute)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Serve: %v; want %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "already ticking") || strings.Contains(err.Error(), "pid 0") {
				t.Fatalf("invented liveness: %v", err)
			}
		})
	}
}
