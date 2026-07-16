package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// goldenRoutes is the committed contract: the canonical shipped routes shape
// (a gate-log + ship-receipts source, a slack channel with a placeholder token,
// a toast channel, routes, and a non-drop catch_all). It freezes the shipped
// schema against this binary — the class of drift that took the notification
// plane silently dead for ~17h when the live routes file gained fields the
// binary's structs lacked and DisallowUnknownFields rejected the whole file.
const goldenRoutes = "testdata/routes.golden.json"

func TestConfigLoadAcceptsShippedGoldenRoutes(t *testing.T) {
	cfg, err := Load(goldenRoutes)
	if err != nil {
		t.Fatalf("the shipped golden routes must load against this binary: %v", err)
	}
	if cfg.Version != Version {
		t.Fatalf("version = %d, want %d", cfg.Version, Version)
	}
	kinds := map[string]bool{}
	for _, s := range cfg.Sources {
		kinds[s.Kind] = true
	}
	for _, want := range []string{SourceGateLog, SourceShipReceipts} {
		if !kinds[want] {
			t.Fatalf("golden routes missing a %q source; source kinds = %v", want, kinds)
		}
	}
	types := map[string]string{}
	for name, ch := range cfg.Channels {
		types[name] = ch.Type
	}
	if types["slack"] != ChannelSlack {
		t.Fatalf("channel \"slack\" type = %q, want %q", types["slack"], ChannelSlack)
	}
	if types["toast"] != ChannelToast {
		t.Fatalf("channel \"toast\" type = %q, want %q", types["toast"], ChannelToast)
	}
	// The slack channel must carry token+channel — the exact fields whose
	// absence from the binary's Channel struct caused the outage.
	slack := cfg.Channels["slack"]
	if slack.Token == "" || slack.ChannelID == "" {
		t.Fatalf("golden slack channel must carry token+channel, got %+v", slack)
	}
	if strings.Contains(slack.Token, "xoxb-") && !strings.Contains(slack.Token, "REPLACE") {
		t.Fatalf("golden slack token must be a placeholder, never a real secret: %q", slack.Token)
	}
}

func TestConfigLoadRejectsUnknownField(t *testing.T) {
	// Take the real golden and inject a field the binary's structs do not know.
	// DisallowUnknownFields must make this go red — that is the guarantee that
	// the shipped config schema can never again silently lead the binary.
	raw, err := os.ReadFile(goldenRoutes)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["unknown_future_field"] = json.RawMessage(`true`)
	mutated, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(writeConfig(t, string(mutated))); err == nil {
		t.Fatal("Load must reject a routes file carrying a field the binary does not know")
	}
}

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
