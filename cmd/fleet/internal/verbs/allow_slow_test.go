package verbs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

// expensiveFixture points the state root at a temp dir holding two measured rules.
func expensiveFixture(t *testing.T) {
	t.Helper()
	old := fleet.State
	fleet.State = t.TempDir()
	t.Cleanup(func() { fleet.State = old })
	rules := `[{"name":"full unit suite","pattern":"^npx$"},{"name":"project typecheck","pattern":"^tsc$"}]`
	if err := os.WriteFile(fleet.Path("expensive.json"), []byte(rules), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The gate lets an override through and the harness must not refuse it first, so the
// permissions a roled directory projects have to carry the gate's shape — one rule per
// measured command, and never a wildcard that would swallow the lane's own denies.
func TestProjectedPermissionsAllowTheOverrideShape(t *testing.T) {
	expensiveFixture(t)
	target := filepath.Join(t.TempDir(), "settings.json")
	existing := map[string]any{"permissions": map[string]any{"allow": []any{"Bash(fleet take:*)"}}}
	if _, _, err := writeDenies(existing, map[string]any{"denies": []any{"Bash(git push:*)"}}, target); err != nil {
		t.Fatal(err)
	}
	allow := fleet.Strs(fleet.M(fleet.ReadJSON(target), "permissions"), "allow")
	for _, want := range []string{"Bash(FLEET_ALLOW_SLOW=full-unit-suite:*)", "Bash(FLEET_ALLOW_SLOW=project-typecheck:*)"} {
		if !contains(allow, want) {
			t.Fatalf("the override shape for a measured command is not allowed in the seat: %v", allow)
		}
	}
	if contains(allow, "Bash(FLEET_ALLOW_SLOW=*)") {
		t.Fatalf("a universal override allow undoes the lane's per-verb denies: %v", allow)
	}
	if !contains(allow, "Bash(fleet take:*)") {
		t.Fatalf("an allow already in the file was dropped: %v", allow)
	}
}

func TestProjectingTwiceAddsTheAllowOnce(t *testing.T) {
	expensiveFixture(t)
	target := filepath.Join(t.TempDir(), "settings.json")
	existing := map[string]any{}
	for i := 0; i < 3; i++ {
		if _, _, err := writeDenies(existing, map[string]any{}, target); err != nil {
			t.Fatal(err)
		}
		existing = fleet.ReadJSON(target)
	}
	allow := fleet.Strs(fleet.M(fleet.ReadJSON(target), "permissions"), "allow")
	if len(allow) != 2 {
		t.Fatalf("allow=%v", allow)
	}
}
