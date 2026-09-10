package verbs

import (
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// The gate lets an override through and the harness must not refuse it first, so the
// permissions a roled directory projects have to carry the gate's one shape.
func TestProjectedPermissionsAllowTheOverrideShape(t *testing.T) {
	target := filepath.Join(t.TempDir(), "settings.json")
	existing := map[string]any{"permissions": map[string]any{"allow": []any{"Bash(fleet take:*)"}}}
	if _, _, err := writeDenies(existing, map[string]any{"denies": []any{"Bash(git push:*)"}}, target); err != nil {
		t.Fatal(err)
	}
	allow := fleet.Strs(fleet.M(fleet.ReadJSON(target), "permissions"), "allow")
	if !contains(allow, fleet.AllowSlowPattern) {
		t.Fatalf("the override shape is not allowed in the seat: %v", allow)
	}
	if !contains(allow, "Bash(fleet take:*)") {
		t.Fatalf("an allow already in the file was dropped: %v", allow)
	}
}

func TestProjectingTwiceAddsTheAllowOnce(t *testing.T) {
	target := filepath.Join(t.TempDir(), "settings.json")
	existing := map[string]any{}
	for i := 0; i < 3; i++ {
		if _, _, err := writeDenies(existing, map[string]any{}, target); err != nil {
			t.Fatal(err)
		}
		existing = fleet.ReadJSON(target)
	}
	allow := fleet.Strs(fleet.M(fleet.ReadJSON(target), "permissions"), "allow")
	if len(allow) != 1 || allow[0] != fleet.AllowSlowPattern {
		t.Fatalf("allow=%v", allow)
	}
}
