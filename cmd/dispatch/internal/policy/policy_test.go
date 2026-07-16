package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePolicy(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const validBody = `{"version":1,"rules":[` +
	`{"name":"m","match":{"task_class":"mechanical","max_weighted_loc":500,"risk_tier":["T0"]},` +
	`"place":{"engine":"ship-driver","provider":"cursor","model":"grok-4.5","effort":"high","runtime":"cloud"},` +
	`"escalation":{"max_rounds_per_defect_class":2,"escalate_to":"claude-opus-4-8"}}]}`

func TestLoadRejects(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{name: "empty rules", body: `{"version":1,"rules":[]}`, wantErr: "no rules"},
		{name: "unknown version", body: `{"version":2,"rules":[]}`, wantErr: "not supported"},
		{name: "unknown task_class in match", body: `{"version":1,"rules":[` +
			`{"name":"x","match":{"task_class":"mechnical"},` +
			`"place":{"engine":"e","provider":"p","model":"m","effort":"f","runtime":"r"}}]}`, wantErr: "unknown task_class"},
		{name: "malformed json", body: `{"version":1,"rules":[`, wantErr: "parse"},
		{name: "unknown field", body: `{"version":1,"rules":[],"extra":true}`, wantErr: "parse"},
		{name: "rule missing name", body: `{"version":1,"rules":[{"match":{},` +
			`"place":{"engine":"e","provider":"p","model":"m","effort":"f","runtime":"r"}}]}`, wantErr: "name is required"},
		{name: "rule missing place", body: `{"version":1,"rules":[{"name":"x","match":{}}]}`, wantErr: "place requires"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(writePolicy(t, tt.body))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load() error = %v, want contains %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("Load() on a missing file must error")
	}
}

func TestLoadValid(t *testing.T) {
	loaded, err := Load(writePolicy(t, validBody))
	if err != nil {
		t.Fatalf("valid policy must load: %v", err)
	}
	if len(loaded.Policy.Rules) != 1 || loaded.Policy.Rules[0].Name != "m" {
		t.Fatalf("unexpected rules: %+v", loaded.Policy.Rules)
	}
	if loaded.SHA256 == "" {
		t.Fatal("a loaded policy must carry a sha256")
	}
}

func TestSHA256IsStableAndContentPinned(t *testing.T) {
	p := writePolicy(t, validBody)
	first, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if first.SHA256 != again.SHA256 {
		t.Fatalf("hash must be stable across reads: %s != %s", first.SHA256, again.SHA256)
	}
	// An edit (even whitespace, changing exact bytes) must change the hash.
	p2 := writePolicy(t, validBody+"\n")
	other, err := Load(p2)
	if err != nil {
		t.Fatal(err)
	}
	if other.SHA256 == first.SHA256 {
		t.Fatal("a content edit must change the sha256")
	}
}

func TestValidClass(t *testing.T) {
	for _, c := range []string{ClassMechanical, ClassAnalytical, ClassGenerative} {
		if !ValidClass(c) {
			t.Fatalf("%q must be a valid class", c)
		}
	}
	for _, c := range []string{"", "mechnical", "size", "large"} {
		if ValidClass(c) {
			t.Fatalf("%q must not be a valid class", c)
		}
	}
}

func TestHasCatchAll(t *testing.T) {
	loaded, err := Load(writePolicy(t, validBody))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Policy.HasCatchAll() {
		t.Fatal("a policy with only a constrained rule has no catch-all")
	}
	withCatch := `{"version":1,"rules":[{"name":"c","match":{},` +
		`"place":{"engine":"e","provider":"p","model":"m","effort":"f","runtime":"r"}}]}`
	loaded2, err := Load(writePolicy(t, withCatch))
	if err != nil {
		t.Fatal(err)
	}
	if !loaded2.Policy.HasCatchAll() {
		t.Fatal("a policy with a match:{} rule has a catch-all")
	}
}
