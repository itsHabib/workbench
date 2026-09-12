package verbs

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// Tools read decisions as JSON: the table pads its columns for people, so a
// consumer that parsed it could never match "kind subject: text" (standup's
// already-decided skip never fired). Retired decisions are not in force.
func TestDecisionsJSONListsOnlyDecisionsInForce(t *testing.T) {
	oldState, oldOut := fleet.State, Out
	fleet.State = t.TempDir()
	var out bytes.Buffer
	Out = &out
	t.Cleanup(func() { fleet.State = oldState; Out = oldOut })

	if err := cmdDecide("rule", "ivy", "two fix-rounds then the judge"); err != nil {
		t.Fatal(err)
	}
	if err := cmdDecide("park", "billing", "product direction"); err != nil {
		t.Fatal(err)
	}
	if err := cmdUndecide("d2"); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := cmdDecisions(true); err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("decisions --json is not JSON: %v\n%s", err, out.String())
	}
	if len(rows) != 1 || rows[0]["id"] != "d1" || rows[0]["kind"] != "rule" || rows[0]["subject"] != "ivy" || rows[0]["text"] != "two fix-rounds then the judge" {
		t.Fatalf("want only d1 in force with its fields, got %v", rows)
	}
}
