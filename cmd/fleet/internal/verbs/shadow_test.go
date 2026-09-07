package verbs

import (
	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"os"
	"testing"
)

func TestShadowRowsCombinesLegacyAndTaggedNewHistory(t *testing.T) {
	old := fleet.State
	fleet.State = t.TempDir()
	t.Cleanup(func() { fleet.State = old })
	if err := os.WriteFile(fleet.Path("shadow.jsonl"), []byte("{\"at\":1}\n{\"at\":3}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleet.Path("events.jsonl"), []byte("{\"at\":4,\"shadow\":false}\n{\"at\":5,\"shadow\":true}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rows := shadowRows(2)
	if len(rows) != 2 || fleet.F(rows[0], "at") != 3 || fleet.F(rows[1], "at") != 5 {
		t.Fatal(rows)
	}
}
