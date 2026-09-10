package watch

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestTickPublishesReport(t *testing.T) {
	oldState, oldOrg := fleet.State, fleet.OrgState
	fleet.State = t.TempDir()
	fleet.OrgState = t.TempDir()
	t.Cleanup(func() { fleet.State = oldState; fleet.OrgState = oldOrg })
	t.Setenv("FLEET_GITHUB", "off")
	if _, err := Tick(time.Minute); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir(), "report.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "# Fleet report") || !strings.Contains(string(b), "Coverage: events.jsonl") {
		t.Fatal(string(b))
	}
}

func TestReportFailureDoesNotSuppressNotification(t *testing.T) {
	oldState, oldOrg := fleet.State, fleet.OrgState
	fleet.State = t.TempDir()
	fleet.OrgState = t.TempDir()
	t.Cleanup(func() { fleet.State = oldState; fleet.OrgState = oldOrg })
	t.Setenv("FLEET_GITHUB", "off")
	sink := filepath.Join(t.TempDir(), "notification.json")
	t.Setenv("FLEET_TEST_NOTIFY_PATH", sink)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_NOTIFY", strconv.Quote(exe)+" -test.run=^TestNotifyRecorder$")
	if err := fleet.WriteJSON(fleet.Path("sessions", "s.json"), fleet.Rec{"session": "s", "last_event_at": fleet.Now(), "turn_open": true}); err != nil {
		t.Fatal(err)
	}
	if err := fleet.WriteLease("repo:r:topic", fleet.LeaseRecord("repo:r:topic", "s", "", "", nil)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir(), "report.md"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Tick(time.Minute); err != nil {
		t.Fatalf("derived report broke tick: %v", err)
	}
	b, err := os.ReadFile(sink)
	if err != nil {
		t.Fatalf("notification missing: %v", err)
	}
	if !strings.Contains(string(b), "undeclared") {
		t.Fatal(string(b))
	}
	b, err = os.ReadFile(fleet.Path("hook-errors.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "watch report:") {
		t.Fatal("report error not recorded")
	}
}
func TestNotifyRecorder(_ *testing.T) {
	p := os.Getenv("FLEET_TEST_NOTIFY_PATH")
	if p == "" {
		return
	}
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(2)
	}
	if err := os.WriteFile(p, b, 0600); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}
