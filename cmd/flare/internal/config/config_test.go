package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "routes.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCatchAllCannotDrop(t *testing.T) {
	// A drop catch_all would silently swallow unrouted events — the one thing
	// the catch_all exists to prevent. Both the literal drop channel and a
	// configured channel of type drop must be rejected as catch_all.
	for _, catchAll := range []string{"drop", "quiet"} {
		body := `{"version":1,"sources":[{"name":"gate","kind":"gate-log","path":"/x"}],` +
			`"channels":{"quiet":{"type":"drop"}},"catch_all":"` + catchAll + `"}`
		if _, err := Load(writeConfig(t, body)); err == nil {
			t.Fatalf("catch_all=%q must be rejected: it cannot be a drop channel", catchAll)
		}
	}
}

func TestValidCatchAllLoads(t *testing.T) {
	body := `{"version":1,"sources":[{"name":"gate","kind":"gate-log","path":"/x"}],` +
		`"channels":{"toast":{"type":"toast"}},"catch_all":"toast"}`
	if _, err := Load(writeConfig(t, body)); err != nil {
		t.Fatalf("a valid config must load: %v", err)
	}
}
