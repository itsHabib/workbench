package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestSlackRequiresCredentialAndChannel(t *testing.T) {
	tests := []struct {
		name    string
		slack   string
		wantErr string
	}{
		{name: "token", slack: `{"type":"slack","channel":"C123"}`, wantErr: "slack requires token"},
		{name: "channel", slack: `{"type":"slack","token":"placeholder"}`, wantErr: "slack requires channel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := `{"version":1,"sources":[{"name":"gate","kind":"gate-log","path":"/x"}],` +
				`"channels":{"phone":` + tt.slack + `},"catch_all":"phone"}`
			_, err := Load(writeConfig(t, body))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Load() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidSlackChannelLoads(t *testing.T) {
	body := `{"version":1,"sources":[{"name":"gate","kind":"gate-log","path":"/x"}],` +
		`"channels":{"phone":{"type":"slack","token":"placeholder","channel":"C123"}},"catch_all":"phone"}`
	cfg, err := Load(writeConfig(t, body))
	if err != nil {
		t.Fatalf("a valid Slack channel must load: %v", err)
	}
	if cfg.Channels["phone"].ChannelID != "C123" {
		t.Fatalf("Slack channel = %q, want C123", cfg.Channels["phone"].ChannelID)
	}
}
