package serve

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/custody/internal/grant"
	"github.com/itsHabib/workbench/cmd/custody/internal/manifest"
)

// TestNilTransportDisablesCompression pins the production wiring: an engine built
// with no Transport must run over a transport with transparent gzip disabled, so
// the proxy streams the upstream's bytes and Content-Encoding verbatim (spec §6)
// instead of silently decoding a gzip body and dropping its encoding headers.
func TestNilTransportDisablesCompression(t *testing.T) {
	man, err := manifest.Load(strings.NewReader(manifestJSON("https://upstream.test")))
	if err != nil {
		t.Fatalf("manifest.Load: %v", err)
	}
	grants, err := grant.NewStore(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("grant.NewStore: %v", err)
	}
	engine, err := New(Config{
		Manifest:  man,
		Grants:    grants,
		Secrets:   fakeSecrets{m: map[string]string{}},
		LogWriter: &bytes.Buffer{},
		// Transport deliberately nil: assert the production default.
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr, ok := engine.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("default transport is %T, want *http.Transport", engine.client.Transport)
	}
	if !tr.DisableCompression {
		t.Fatal("New with nil Transport must disable transparent gzip (verbatim streaming, spec §6)")
	}
}

// TestForwardStreamsGzipVerbatim proves the end-to-end property: when the upstream
// answers with a gzip-encoded body, custody relays the exact gzip bytes and the
// Content-Encoding header untouched. Under the pre-fix default transport the Go
// client would transparently decode the body and strip Content-Encoding, so this
// would fail.
func TestForwardStreamsGzipVerbatim(t *testing.T) {
	plain := `{"ok":true,"n":42}`
	var gzbuf bytes.Buffer
	gw := gzip.NewWriter(&gzbuf)
	if _, err := gw.Write([]byte(plain)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	want := gzbuf.Bytes()

	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(want)
	}))
	t.Cleanup(up.Close)

	man, err := manifest.Load(strings.NewReader(manifestJSON(up.URL)))
	if err != nil {
		t.Fatalf("manifest.Load: %v", err)
	}
	grants, err := grant.NewStore(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("grant.NewStore: %v", err)
	}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	_, token, err := grants.Mint("tracker", []string{"read"}, time.Hour, "test", func() time.Time { return now })
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	// Mirror the production default transport (DisableCompression) while trusting
	// the test server's certificate.
	tr := up.Client().Transport.(*http.Transport).Clone()
	tr.DisableCompression = true

	engine, err := New(Config{
		Manifest:       man,
		ManifestDigest: "test-digest",
		Grants:         grants,
		Secrets:        fakeSecrets{m: map[string]string{"tracker-pat": testSecret}},
		LogWriter:      &bytes.Buffer{},
		Transport:      tr,
		Now:            func() time.Time { return now },
		NewRequestID:   func() string { return "req_test" },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	h := http.Header{}
	h.Set(grantHeader, token)
	w := do(engine, "GET", "/tracker/rest/api/2/issue/PROJ-1", h, nil)

	if got := w.Code; got != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", got, w.Body.String())
	}
	if got := w.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip (verbatim relay)", got)
	}
	if !bytes.Equal(w.Body.Bytes(), want) {
		t.Fatalf("body not relayed verbatim: got %d bytes, want %d (decoded upstream?)", w.Body.Len(), len(want))
	}
}

// TestSanitizeHeaderValueStripsCRLF guards the X-Custody-Note first-use header
// (FR6): an operator note carrying CR/LF must not be able to split the response
// into an injected second header.
func TestSanitizeHeaderValueStripsCRLF(t *testing.T) {
	got := sanitizeHeaderValue("Work tracker.\r\nX-Injected: evil")
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("sanitizeHeaderValue left CR/LF in %q", got)
	}
	if strings.Contains(got, "X-Injected") == false {
		t.Fatalf("sanitizeHeaderValue dropped content, got %q", got)
	}
}
