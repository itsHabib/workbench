package verbs

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

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
