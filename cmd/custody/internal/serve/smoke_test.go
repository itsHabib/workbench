package serve

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/custody/internal/grant"
	"github.com/itsHabib/workbench/cmd/custody/internal/manifest"
)

// TestSmokeFirstKeyEndToEnd wires the full custody chain the way `custody serve`
// runs it — manifest + real grant store + credential store + engine over an
// httptest upstream — and drives the three outcomes an operator wiring their
// first real key must see: a granted request PASSES with the credential
// injected, an over-scope request is DENIED with a remedy that names the grant
// command, and an EXPIRED grant is refused. The mint key is bootstrapped through
// the exact -init-guarded path the grant verb now uses (RequireMintKey(true)
// then Mint), so the smoke reflects the real first-run flow rather than a
// pre-seeded key.
func TestSmokeFirstKeyEndToEnd(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	cp := &capture{}
	up := upstreamOK(t, cp)

	// A read-scoped grant, minted on a fresh key dir through the first-run
	// bootstrap opt-in.
	engine, token, log := smokeEngine(t, up, []string{"read"}, time.Hour, now)
	hdr := http.Header{grantHeader: {token}}

	// (a) Granted request PASSES: 200, the credential is injected, and the
	// upstream actually saw the injected header.
	pass := do(engine, "GET", "/tracker/rest/api/2/issue/PROJ-1", hdr, nil)
	if pass.Code != http.StatusOK {
		t.Fatalf("granted request status = %d, want 200; body=%s", pass.Code, pass.Body.String())
	}
	if got := cp.last.Header.Get("Authorization"); got != "Bearer "+testSecret {
		t.Fatalf("upstream Authorization = %q, want the injected bearer credential", got)
	}
	if v := lastLog(t, log).Verdict; v != verdictPass {
		t.Fatalf("granted request verdict = %q, want pass", v)
	}

	// (b) Over-scope request DENIED: a POST comment under a read-only grant is a
	// 403 with a {code,reason,remedy,request_id} body whose remedy names the
	// grant command for the action that WOULD cover it, and never reaches
	// upstream.
	callsBefore := cp.calls
	denied := do(engine, "POST", "/tracker/rest/api/2/issue/PROJ-1/comment", hdr, strings.NewReader("{}"))
	if denied.Code != http.StatusForbidden {
		t.Fatalf("over-scope request status = %d, want 403", denied.Code)
	}
	body := decodeErr(t, denied) // also asserts request_id is present
	if body.Code == "" || body.Reason == "" || body.Remedy == "" {
		t.Fatalf("denial body missing a required field: %+v", body)
	}
	if !strings.Contains(body.Remedy, "grant") || !strings.Contains(body.Remedy, "-actions comment") {
		t.Fatalf("remedy must name the grant command for the comment action: %q", body.Remedy)
	}
	if cp.calls != callsBefore {
		t.Fatal("a denied request must never reach upstream")
	}

	// (c) EXPIRED grant refused: advance the engine clock past the TTL and the
	// same granted read is now a 401 refused_expired with a grant-command remedy.
	engine.now = func() time.Time { return now.Add(2 * time.Hour) }
	expired := do(engine, "GET", "/tracker/rest/api/2/issue/PROJ-1", hdr, nil)
	if expired.Code != http.StatusUnauthorized {
		t.Fatalf("expired grant status = %d, want 401", expired.Code)
	}
	exp := decodeErr(t, expired)
	if exp.Code != "refused_expired" {
		t.Fatalf("expired grant code = %q, want refused_expired", exp.Code)
	}
	if !strings.Contains(exp.Remedy, "grant") {
		t.Fatalf("expired remedy should name the grant command: %q", exp.Remedy)
	}
}

// smokeEngine builds the chain custody serves at runtime and bootstraps the mint
// key through the -init-guarded path the grant verb uses: on a fresh key dir
// RequireMintKey(true) is the opt-in that lets the first Mint create the key
// (RequireMintKey(false) would refuse with mint_key_missing). It returns the
// engine, a live grant token scoped to actions, and the artifact log buffer.
func smokeEngine(t *testing.T, up *httptest.Server, actions []string, ttl time.Duration, now time.Time) (*Engine, string, *bytes.Buffer) {
	t.Helper()
	man, err := manifest.Load(strings.NewReader(manifestJSON(up.URL)))
	if err != nil {
		t.Fatalf("manifest.Load: %v", err)
	}
	grants, err := grant.NewStore(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("grant.NewStore: %v", err)
	}
	if err := grants.RequireMintKey(true); err != nil {
		t.Fatalf("RequireMintKey(true): %v", err)
	}
	clock := func() time.Time { return now }
	_, token, err := grants.Mint("tracker", actions, ttl, "smoke", clock)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	log := &bytes.Buffer{}
	secrets := fakeSecrets{m: map[string]string{"tracker-pat": testSecret}}
	engine, err := New(Config{
		Manifest:       man,
		ManifestDigest: "smoke-digest",
		Grants:         grants,
		Secrets:        secrets,
		LogWriter:      log,
		Transport:      up.Client().Transport,
		Now:            clock,
		NewRequestID:   func() string { return "req_smoke" },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return engine, token, log
}
