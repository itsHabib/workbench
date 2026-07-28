package verify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

func TestCloudModelAdaptsToolUseToContentString(t *testing.T) {
	input := map[string]any{
		"bucket":     "real-break",
		"evidence":   "parse_test.go:42: got 3, want 4",
		"why":        "TestParse assertion",
		"confidence": 0.9,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"content":[{"type":"tool_use","id":"tu_1","name":"structured_output","input":%s}]}`, mustJSON(t, input))
	}))
	t.Cleanup(srv.Close)

	m := &cloudModel{
		model:  cloudModelDefault,
		apiKey: "test-key",
		url:    srv.URL,
		client: srv.Client(),
	}
	content, err := m.chat(context.Background(), ciPrompt, "log excerpt", ciSchema)
	if err != nil {
		t.Fatal(err)
	}
	var adv ciAdvisory
	if err := json.Unmarshal([]byte(content), &adv); err != nil {
		t.Fatalf("unmarshal adapted content: %v", err)
	}
	if adv.Bucket != "real-break" || adv.Evidence != input["evidence"] {
		t.Fatalf("adapter content: %+v", adv)
	}
}

func TestCloudModelSurfacesAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"bad key"}}`)
	}))
	t.Cleanup(srv.Close)

	m := &cloudModel{
		model:  cloudModelDefault,
		apiKey: "test-key",
		url:    srv.URL,
		client: srv.Client(),
	}
	_, err := m.chat(context.Background(), ciPrompt, "log excerpt", ciSchema)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Fatalf("expected status in error, got: %v", err)
	}
}

func TestResolveAnthropicURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		base string
		want string
	}{
		{name: "direct fallback", want: anthropicDirectURL},
		{name: "origin", base: "https://example.invalid", want: "https://example.invalid/v1/messages"},
		{name: "path prefix", base: "https://example.invalid/provider/anthropic", want: "https://example.invalid/provider/anthropic/v1/messages"},
		{name: "trailing slash", base: "https://example.invalid/provider/", want: "https://example.invalid/provider/v1/messages"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveAnthropicURL(tt.base)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("resolveAnthropicURL(%q) = %q, want %q", tt.base, got, tt.want)
			}
		})
	}
}

func TestResolveAnthropicURLRejectsInvalidBase(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		base string
	}{
		{name: "malformed", base: "://bad"},
		{name: "relative", base: "provider/anthropic"},
		{name: "unsupported scheme", base: "ftp://example.invalid/provider"},
		{name: "userinfo", base: "https://user:pass@example.invalid/provider"},
		{name: "query", base: "https://example.invalid/provider?route=anthropic"},
		{name: "fragment", base: "https://example.invalid/provider#anthropic"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := resolveAnthropicURL(tt.base)
			if err == nil {
				t.Fatalf("resolveAnthropicURL(%q) unexpectedly succeeded", tt.base)
			}
			if strings.Contains(err.Error(), tt.base) {
				t.Fatalf("error leaked configured base: %v", err)
			}
		})
	}
}

func TestCloudModelSendsAnthropicHeaders(t *testing.T) {
	t.Parallel()
	const apiKey = "placeholder-key"
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if got := r.Header.Get("x-api-key"); got != apiKey {
			t.Errorf("x-api-key = %q, want placeholder", got)
		}
		if got := r.Header.Get("anthropic-version"); got != anthropicVersion {
			t.Errorf("anthropic-version = %q, want %q", got, anthropicVersion)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization must be empty, got %q", got)
		}
		fmt.Fprint(w, `{"content":[{"type":"tool_use","name":"structured_output","input":{"ok":true}}]}`)
	}))
	t.Cleanup(srv.Close)

	m := &cloudModel{
		model:   "model-placeholder",
		apiKey:  apiKey,
		url:     srv.URL + "/provider/v1/messages",
		gateway: true,
		client:  srv.Client(),
	}
	if _, err := m.chat(context.Background(), "system", "user", json.RawMessage(`{"type":"object"}`)); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/provider/v1/messages" {
		t.Fatalf("request path = %q", gotPath)
	}
	if got := m.impl(); got != "model-placeholder" {
		t.Fatalf("impl = %q", got)
	}
	if strings.Contains(m.impl(), srv.URL) || strings.Contains(m.impl(), apiKey) {
		t.Fatalf("impl leaked configuration: %q", m.impl())
	}
}

func TestCloudModelGatewayAuthErrorIsRedacted(t *testing.T) {
	t.Parallel()
	const apiKey = "placeholder-key"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, "rejected %s with %s", r.Host, r.Header.Get("x-api-key"))
	}))
	t.Cleanup(srv.Close)

	m := &cloudModel{
		model:   "model-placeholder",
		apiKey:  apiKey,
		url:     srv.URL,
		gateway: true,
		client:  srv.Client(),
	}
	_, err := m.chat(context.Background(), "system", "user", json.RawMessage(`{"type":"object"}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "re-authenticate") {
		t.Fatalf("error must direct re-authentication: %v", err)
	}
	if strings.Contains(err.Error(), apiKey) || strings.Contains(err.Error(), srv.URL) {
		t.Fatalf("error leaked gateway configuration: %v", err)
	}
}

func TestCloudModelGatewayStatusBodyIsRedacted(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, "upstream route: %s", r.Host)
	}))
	t.Cleanup(srv.Close)

	m := &cloudModel{
		model:   "model-placeholder",
		apiKey:  "placeholder-key",
		url:     srv.URL,
		gateway: true,
		client:  srv.Client(),
	}
	_, err := m.chat(context.Background(), "system", "user", json.RawMessage(`{"type":"object"}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("error must include status: %v", err)
	}
	if strings.Contains(err.Error(), srv.URL) || strings.Contains(err.Error(), "upstream route") {
		t.Fatalf("error leaked gateway response: %v", err)
	}
}

func TestCloudModelGatewayEnvelopeErrorIsRedacted(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"type":"error","error":{"type":"upstream_error","message":"route %s failed"}}`, r.Host)
	}))
	t.Cleanup(srv.Close)

	m := &cloudModel{
		model:   "model-placeholder",
		apiKey:  "placeholder-key",
		url:     srv.URL,
		gateway: true,
		client:  srv.Client(),
	}
	_, err := m.chat(context.Background(), "system", "user", json.RawMessage(`{"type":"object"}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Error() != "anthropic: gateway response error" {
		t.Fatalf("gateway envelope error must be stable and redacted: %v", err)
	}
}

func TestCloudModelGatewayTransportErrorIsRedacted(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := srv.URL
	srv.Close()

	m := &cloudModel{
		model:   "model-placeholder",
		apiKey:  "placeholder-key",
		url:     endpoint,
		gateway: true,
		client:  srv.Client(),
	}
	_, err := m.chat(context.Background(), "system", "user", json.RawMessage(`{"type":"object"}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "request failed") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), endpoint) {
		t.Fatalf("error leaked gateway endpoint: %v", err)
	}
}

func TestCloudModelGatewayDoesNotFollowRedirect(t *testing.T) {
	t.Parallel()
	hit := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		hit <- r.Header.Get("x-api-key")
	}))
	t.Cleanup(target.Close)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(gateway.Close)

	m := &cloudModel{
		model:   "model-placeholder",
		apiKey:  "placeholder-key",
		url:     gateway.URL,
		gateway: true,
		client:  cloudHTTPClient(true),
	}
	_, err := m.chat(context.Background(), "system", "user", json.RawMessage(`{"type":"object"}`))
	if err == nil || !strings.Contains(err.Error(), "302") {
		t.Fatalf("expected redacted redirect status, got %v", err)
	}
	if strings.Contains(err.Error(), target.URL) {
		t.Fatalf("error leaked redirect target: %v", err)
	}
	select {
	case key := <-hit:
		t.Fatalf("redirect target received x-api-key %q", key)
	default:
	}
}

type recordingModel struct {
	called bool
}

func (m *recordingModel) chat(_ context.Context, _, _ string, _ json.RawMessage) (string, error) {
	m.called = true
	return `{"headline":"x","severity":"low","verdict":"nit","confidence":0.9}`, nil
}

func (m *recordingModel) impl() string { return "recording" }

func TestReviewsBackendSelection(t *testing.T) {
	t.Run("default local when nil", func(t *testing.T) {
		got, err := ModelBackend("local")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.(*localModel); !ok {
			t.Fatalf("expected localModel, got %T", got)
		}
	})
	t.Run("selected model invoked", func(t *testing.T) {
		rec := &recordingModel{}
		st, err := state.Open(t.TempDir(), time.Now)
		if err != nil {
			t.Fatal(err)
		}
		evd, err := st.Append(state.KindEvidence, "run_t", nil, map[string]any{
			"comments": []map[string]any{
				{"author": "bot[bot]", "is_bot": true, "body": "nit: rename foo"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Reviews(st, "run_t", evd.ID, subj, rec); err != nil {
			t.Fatal(err)
		}
		if !rec.called {
			t.Fatal("expected injected model to be called")
		}
	})
}

func TestCIClassifyBackendSelection(t *testing.T) {
	t.Run("default local when nil", func(t *testing.T) {
		got, err := ModelBackend("")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.(*localModel); !ok {
			t.Fatalf("expected localModel, got %T", got)
		}
	})
	t.Run("cloud backend", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "test-key")
		t.Setenv("ANTHROPIC_BASE_URL", "")
		t.Setenv("GATE_CLOUD_MODEL", "ignored-in-direct-mode")
		got, err := ModelBackend("cloud")
		if err != nil {
			t.Fatal(err)
		}
		cloud, ok := got.(*cloudModel)
		if !ok {
			t.Fatalf("expected cloudModel, got %T", got)
		}
		if cloud.model != cloudModelDefault || cloud.url != anthropicDirectURL || cloud.gateway {
			t.Fatalf("direct cloud model changed: %+v", cloud)
		}
		if cloud.client.CheckRedirect != nil {
			t.Fatal("direct cloud model must retain default redirect behavior")
		}
	})
	t.Run("gateway requires model", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "test-key")
		t.Setenv("ANTHROPIC_BASE_URL", "https://example.invalid/provider")
		t.Setenv("GATE_CLOUD_MODEL", "")
		_, err := ModelBackend("cloud")
		if err == nil || !strings.Contains(err.Error(), "GATE_CLOUD_MODEL") {
			t.Fatalf("expected missing model error, got %v", err)
		}
	})
	t.Run("gateway configuration", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "test-key")
		t.Setenv("ANTHROPIC_BASE_URL", "https://example.invalid/provider")
		t.Setenv("GATE_CLOUD_MODEL", "model-placeholder")
		got, err := ModelBackend("cloud")
		if err != nil {
			t.Fatal(err)
		}
		cloud, ok := got.(*cloudModel)
		if !ok {
			t.Fatalf("expected cloudModel, got %T", got)
		}
		if cloud.model != "model-placeholder" {
			t.Fatalf("model = %q", cloud.model)
		}
		if cloud.url != "https://example.invalid/provider/v1/messages" {
			t.Fatalf("url = %q", cloud.url)
		}
		if !cloud.gateway {
			t.Fatal("gateway mode not recorded")
		}
		if cloud.client.CheckRedirect == nil {
			t.Fatal("gateway redirect policy not installed")
		}
	})
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
