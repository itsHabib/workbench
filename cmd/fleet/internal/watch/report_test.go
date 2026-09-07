package watch

import (
	"os"
	"path/filepath"
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
