package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/custody/internal/credstore"
	"github.com/itsHabib/workbench/cmd/custody/internal/grant"
	"github.com/itsHabib/workbench/cmd/custody/internal/manifest"
)

const testSecret = "s3cr3t-value-DO-NOT-LOG"

// fakeSecrets is an in-memory credstore.Store for tests.
type fakeSecrets struct{ m map[string]string }

func (f fakeSecrets) Get(ref string) ([]byte, error) {
	s, ok := f.m[ref]
	if !ok {
		return nil, credstore.ErrSecretUnavailable
	}
	return []byte(s), nil
}

func (f fakeSecrets) Set(ref string, secret []byte) error {
	f.m[ref] = string(secret)
	return nil
}

// capture records what the upstream received, so tests assert on the forwarded
// request without trusting the response.
type capture struct {
	mu      sync.Mutex
	calls   int
	last    *http.Request
	lastRaw string
}

func (c *capture) record(r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.last = r.Clone(r.Context())
	c.lastRaw = r.RequestURI
}

// harness wires an engine over a TLS upstream, a real grant store, a fake secret
// store, an in-memory log, and a fixed clock.
type harness struct {
	engine   *Engine
	token    string
	log      *bytes.Buffer
	now      time.Time
	secrets  fakeSecrets
	stateDir string
}

func manifestJSON(upstream string) string {
	return fmt.Sprintf(`{
	  "version": 1,
	  "keys": {
	    "tracker": {
	      "secret": "wincred:tracker-pat",
	      "upstream": %q,
	      "inject": [{"kind":"header","name":"Authorization","template":"Bearer {secret}"}],
	      "actions": {
	        "read": { "rules": [
	          {"methods":["GET"], "path":"/rest/api/2/issue/PROJ-*"},
	          {"methods":["GET"], "path":"/rest/api/2/project/PROJ/versions",
	           "query": {"state": {"equals":"released","occurs":"once"}}}
	        ]},
	        "comment": { "rules": [
	          {"methods":["POST"], "path":"/rest/api/2/issue/PROJ-*/comment"}
	        ]}
	      },
	      "note": "Work tracker. PROJ only."
	    }
	  }
	}`, upstream)
}

func newHarness(t *testing.T, upstream *httptest.Server, actions []string) *harness {
	t.Helper()
	man, err := manifest.Load(strings.NewReader(manifestJSON(upstream.URL)))
	if err != nil {
		t.Fatalf("manifest.Load: %v", err)
	}
	stateDir := t.TempDir()
	keyDir := t.TempDir()
	grants, err := grant.NewStore(stateDir, keyDir)
	if err != nil {
		t.Fatalf("grant.NewStore: %v", err)
	}
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	_, token, err := grants.Mint("tracker", actions, time.Hour, "test", func() time.Time { return now })
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	logBuf := &bytes.Buffer{}
	secrets := fakeSecrets{m: map[string]string{"tracker-pat": testSecret}}
	engine, err := New(Config{
		Manifest:       man,
		ManifestDigest: "test-digest",
		Grants:         grants,
		Secrets:        secrets,
		LogWriter:      logBuf,
		Transport:      upstream.Client().Transport,
		Now:            func() time.Time { return now },
		NewRequestID:   func() string { return "req_test" },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &harness{engine: engine, token: token, log: logBuf, now: now, secrets: secrets, stateDir: stateDir}
}

// upstreamOK returns a TLS upstream that records requests and answers 200.
func upstreamOK(t *testing.T, cp *capture) *httptest.Server {
	t.Helper()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cp.record(r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func do(engine *Engine, method, target string, header http.Header, body io.Reader) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:8127"+target, body)
	r.RequestURI = target
	for k, vs := range header {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, r)
	return w
}

func lastLog(t *testing.T, buf *bytes.Buffer) logRecord {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var rec logRecord
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &rec); err != nil {
		t.Fatalf("unmarshal log: %v (line %q)", err, lines[len(lines)-1])
	}
	return rec
}

// Flow A — happy path.
func TestFlowAHappyPath(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	h := newHarness(t, up, []string{"read"})

	w := do(h.engine, "GET", "/tracker/rest/api/2/issue/PROJ-123",
		http.Header{grantHeader: {h.token}}, nil)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	if got := cp.last.Header.Get("Authorization"); got != "Bearer "+testSecret {
		t.Fatalf("upstream Authorization = %q, want injected bearer", got)
	}
	if cp.last.Host != up.Listener.Addr().String() {
		t.Fatalf("upstream Host = %q, want %q (forced from manifest)", cp.last.Host, up.Listener.Addr().String())
	}
	if cp.last.URL.Path != "/rest/api/2/issue/PROJ-123" {
		t.Fatalf("forwarded path = %q", cp.last.URL.Path)
	}
	if w.Header().Get("X-Custody-Request-Id") != "req_test" {
		t.Fatal("missing X-Custody-Request-Id")
	}
	if w.Header().Get("X-Custody-Note") == "" {
		t.Fatal("first use should carry X-Custody-Note")
	}
	rec := lastLog(t, h.log)
	if rec.Verdict != verdictPass || rec.RuleFired != "read[0]" || rec.UpstreamStatus != 200 {
		t.Fatalf("log = %+v", rec)
	}
}

func TestNoteOnlyOnFirstUse(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	h := newHarness(t, up, []string{"read"})
	hdr := http.Header{grantHeader: {h.token}}

	first := do(h.engine, "GET", "/tracker/rest/api/2/issue/PROJ-1", hdr, nil)
	second := do(h.engine, "GET", "/tracker/rest/api/2/issue/PROJ-2", hdr, nil)
	if first.Header().Get("X-Custody-Note") == "" {
		t.Fatal("first use must carry the note")
	}
	if second.Header().Get("X-Custody-Note") != "" {
		t.Fatal("second use must not repeat the note")
	}
}

// Flow B — denial with remedy.
func TestFlowBDeniedWithRemedy(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	h := newHarness(t, up, []string{"read"}) // read grant, POST comment

	w := do(h.engine, "POST", "/tracker/rest/api/2/issue/PROJ-1/comment",
		http.Header{grantHeader: {h.token}}, strings.NewReader("{}"))

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	body := decodeErr(t, w)
	if body.Code != "denied_no_action_match" {
		t.Fatalf("code = %q", body.Code)
	}
	if !strings.Contains(body.Remedy, "-actions comment") {
		t.Fatalf("remedy should name the comment action: %q", body.Remedy)
	}
	if cp.calls != 0 {
		t.Fatal("a denied request must never reach upstream")
	}
	if lastLog(t, h.log).Verdict != verdictDenied {
		t.Fatal("verdict should be denied")
	}
}

// Flow C — canonical-target refusal (adversarial encodings never forward).
func TestFlowCCanonicalRefusal(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	h := newHarness(t, up, []string{"read"})
	hdr := http.Header{grantHeader: {h.token}}

	for _, target := range []string{
		"/tracker/rest/api/2/issue/PROJ-1%2F..%2FOTHER-9",
		"/tracker/rest/api/2/issue/PROJ-1%252F..%252FOTHER-9",
		"/tracker/rest/%00/PROJ-1",
	} {
		w := do(h.engine, "GET", target, hdr, nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("target %q status = %d, want 400", target, w.Code)
		}
	}
	// A dot-segment that resolves to OTHER-9 canonicalizes fine but is DENIED by
	// the rule, and never reaches upstream as PROJ-*.
	w := do(h.engine, "GET", "/tracker/rest/api/2/issue/PROJ-1/../OTHER-9", hdr, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("dot-segment status = %d, want 403", w.Code)
	}
	if cp.calls != 0 {
		t.Fatal("no adversarial request should reach upstream")
	}
}

// Flow C — redirects are never followed.
func TestRedirectNotFollowed(t *testing.T) {
	elsewhere := &capture{}
	elseSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere.record(r)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(elseSrv.Close)

	up := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", elseSrv.URL+"/leaked")
		w.WriteHeader(http.StatusFound)
	}))
	t.Cleanup(up.Close)
	h := newHarness(t, up, []string{"read"})

	w := do(h.engine, "GET", "/tracker/rest/api/2/issue/PROJ-1", http.Header{grantHeader: {h.token}}, nil)
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 streamed verbatim", w.Code)
	}
	if w.Header().Get("Location") == "" {
		t.Fatal("Location should be streamed back so the agent decides")
	}
	if elsewhere.calls != 0 {
		t.Fatal("custody must not follow a 302 and re-attach the credential elsewhere")
	}
}

// Flow C — TRACE and CONNECT are denied unconditionally.
func TestMethodDenials(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	h := newHarness(t, up, []string{"read"})
	for _, m := range []string{"TRACE", "CONNECT"} {
		w := do(h.engine, m, "/tracker/rest/api/2/issue/PROJ-1", http.Header{grantHeader: {h.token}}, nil)
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want 405", m, w.Code)
		}
	}
	if cp.calls != 0 {
		t.Fatal("TRACE/CONNECT must never reach upstream")
	}
}

// Header policy — agent headers are allowlisted, injected header replaces, and
// X-Custody-Grant is never forwarded.
func TestHeaderPolicy(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	h := newHarness(t, up, []string{"read"})

	hdr := http.Header{
		grantHeader:     {h.token},
		"Authorization": {"Bearer agent-supplied-should-be-replaced"},
		"Accept":        {"application/json"},
		"X-Secret-Note": {"should-be-dropped"},
	}
	w := do(h.engine, "GET", "/tracker/rest/api/2/issue/PROJ-1", hdr, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if got := cp.last.Header.Get("Authorization"); got != "Bearer "+testSecret {
		t.Fatalf("injected header must replace agent Authorization, got %q", got)
	}
	if cp.last.Header.Get("Accept") != "application/json" {
		t.Fatal("allowlisted Accept should forward")
	}
	if cp.last.Header.Get("X-Secret-Note") != "" {
		t.Fatal("non-allowlisted header must be dropped")
	}
	if cp.last.Header.Get(grantHeader) != "" {
		t.Fatal("X-Custody-Grant must never be forwarded upstream")
	}
}

// Flow D — matched_query is logged for a value-branching rule.
func TestFlowDMatchedQueryLogged(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	h := newHarness(t, up, []string{"read"})

	w := do(h.engine, "GET", "/tracker/rest/api/2/project/PROJ/versions?state=released",
		http.Header{grantHeader: {h.token}}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	rec := lastLog(t, h.log)
	if got := rec.MatchedQuery["state"]; len(got) != 1 || got[0] != "released" {
		t.Fatalf("matched_query = %v", rec.MatchedQuery)
	}
	// Unlisted param is denied by default.
	w = do(h.engine, "GET", "/tracker/rest/api/2/project/PROJ/versions?state=released&expand=x",
		http.Header{grantHeader: {h.token}}, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("unlisted param status = %d, want 403", w.Code)
	}
}

// Flow E — grant expiry mid-session.
func TestFlowEExpired(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	h := newHarness(t, up, []string{"read"})
	// Advance the engine clock past the grant TTL.
	h.engine.now = func() time.Time { return h.now.Add(2 * time.Hour) }

	w := do(h.engine, "GET", "/tracker/rest/api/2/issue/PROJ-1", http.Header{grantHeader: {h.token}}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	body := decodeErr(t, w)
	if body.Code != "refused_expired" {
		t.Fatalf("code = %q, want refused_expired", body.Code)
	}
	if !strings.Contains(body.Remedy, "grant") {
		t.Fatalf("remedy should name the mint command: %q", body.Remedy)
	}
}

// Flow F — secret missing.
func TestFlowFSecretMissing(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	h := newHarness(t, up, []string{"read"})
	delete(h.secrets.m, "tracker-pat")

	w := do(h.engine, "GET", "/tracker/rest/api/2/issue/PROJ-1", http.Header{grantHeader: {h.token}}, nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := decodeErr(t, w)
	if body.Code != "secret_unavailable" {
		t.Fatalf("code = %q", body.Code)
	}
	if !strings.Contains(body.Remedy, "set -name tracker-pat") {
		t.Fatalf("remedy = %q", body.Remedy)
	}
	if cp.calls != 0 {
		t.Fatal("nothing forwarded when the secret is missing")
	}
}

// unknown key -> 404.
func TestUnknownKey(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	h := newHarness(t, up, []string{"read"})
	w := do(h.engine, "GET", "/nope/rest", http.Header{grantHeader: {h.token}}, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
	if decodeErr(t, w).Code != "unknown_key" {
		t.Fatal("code should be unknown_key")
	}
}

// wrong-key grant -> 401 (the dot-segment key-boundary case is safe).
func TestWrongKeyBoundary(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	h := newHarness(t, up, []string{"read"})
	// Resolves to key "evil" which is not the grant's key -> refused/unknown.
	w := do(h.engine, "GET", "/tracker/../tracker2/rest", http.Header{grantHeader: {h.token}}, nil)
	if w.Code != http.StatusNotFound { // tracker2 is not in the manifest
		t.Fatalf("status = %d, want 404 (resolved key not in manifest)", w.Code)
	}
	if cp.calls != 0 {
		t.Fatal("nothing forwarded")
	}
}

// Flow — upstream unreachable -> 502.
func TestUpstreamUnreachable(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	h := newHarness(t, up, []string{"read"})
	up.Close() // now unreachable

	w := do(h.engine, "GET", "/tracker/rest/api/2/issue/PROJ-1", http.Header{grantHeader: {h.token}}, nil)
	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}
	if lastLog(t, h.log).Verdict != verdictUpstreamError {
		t.Fatal("verdict should be upstream_error")
	}
}

func TestNoGrant(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	h := newHarness(t, up, []string{"read"})
	w := do(h.engine, "GET", "/tracker/rest/api/2/issue/PROJ-1", http.Header{}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
	if decodeErr(t, w).Code != "refused_no_grant" {
		t.Fatal("code should be refused_no_grant")
	}
}

// Golden: no secret bytes in any log line or custody-generated response.
func TestNoSecretInLogsOrErrors(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	h := newHarness(t, up, []string{"read"})
	hdr := http.Header{grantHeader: {h.token}, "Authorization": {"Bearer " + testSecret}}

	// A mix of pass and refusals.
	do(h.engine, "GET", "/tracker/rest/api/2/issue/PROJ-1", hdr, nil)
	do(h.engine, "POST", "/tracker/rest/api/2/issue/PROJ-1/comment", hdr, strings.NewReader("x"))
	do(h.engine, "GET", "/tracker/rest/api/2/issue/PROJ-1%2Fx", hdr, nil)

	if strings.Contains(h.log.String(), testSecret) {
		t.Fatal("secret bytes leaked into the log")
	}
	// The grant token (bearer proof) must not appear verbatim in the log either.
	if strings.Contains(h.log.String(), h.token) {
		t.Fatal("grant token leaked into the log")
	}
}

// A depth-2 chain reaching the proxy refuses as refused_chain_depth over HTTP —
// the coded refusal survives the boundary, is a 401, and never forwards.
func TestFlowChainDepthRefused(t *testing.T) {
	cp := &capture{}
	up := upstreamOK(t, cp)
	h := newHarness(t, up, []string{"read"})
	now := func() time.Time { return h.now }

	// A legitimate depth-1 child of the root grant.
	_, childTok, err := h.engine.grants.Derive(h.token, []string{"read"}, time.Hour, "", "test", now)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	// Give the child's parent (the root) a parent of its own in the persisted
	// record — the depth-2 shape an old binary could leave. Validate reads the
	// parent record's parent field without re-signing it, so no re-sign is needed.
	deepenParent(t, h.stateDir, tokenID(t, h.token))

	w := do(h.engine, "GET", "/tracker/rest/api/2/issue/PROJ-1", http.Header{grantHeader: {childTok}}, nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", w.Code, w.Body.String())
	}
	if got := decodeErr(t, w).Code; got != "refused_chain_depth" {
		t.Fatalf("code = %q, want refused_chain_depth", got)
	}
	if cp.calls != 0 {
		t.Fatal("a depth-2 chain must never reach upstream")
	}
}

// tokenID extracts the grant id from a cst2_<id>.<sig> token.
func tokenID(t *testing.T, tok string) string {
	t.Helper()
	body := strings.TrimPrefix(tok, "cst2_")
	id, _, ok := strings.Cut(body, ".")
	if !ok {
		t.Fatalf("malformed token %q", tok)
	}
	return id
}

// deepenParent rewrites the grant record at id to carry a non-empty parent,
// turning any grant derived from it into a depth-2 chain.
func deepenParent(t *testing.T, stateDir, id string) {
	t.Helper()
	path := filepath.Join(stateDir, "grants", id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read grant %s: %v", id, err)
	}
	var rec map[string]any
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatalf("unmarshal grant %s: %v", id, err)
	}
	rec["parent"] = strings.Repeat("a", 32)
	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal grant %s: %v", id, err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write grant %s: %v", id, err)
	}
}

func decodeErr(t *testing.T, w *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v (%s)", err, w.Body.String())
	}
	if body.RequestID == "" {
		t.Fatal("error body missing request_id")
	}
	return body
}
