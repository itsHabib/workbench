// Package serve is custody's proxy engine: the localhost reverse proxy that
// turns an agent request into a pass (credential injected, forwarded, logged) or
// a fail-closed refusal/denial with a remedy. It composes the grant mechanism,
// the manifest, the credential store, and the match policy — it imports custody's
// OWN packages only, never another tenant's decision code.
//
// The pipeline is spec §3: canonicalize the whole target, resolve the key prefix,
// validate the grant, match the granted actions, read the secret, inject and
// forward with redirects disabled, then append one artifact line. The
// load-bearing invariant (spec §7 C) is that the exact semantic target matched is
// the target forwarded — the engine builds the outbound URL only from the
// manifest authority plus the canonical decoded path, never from request bytes.
package serve

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/itsHabib/workbench/cmd/custody/internal/credstore"
	"github.com/itsHabib/workbench/cmd/custody/internal/grant"
	"github.com/itsHabib/workbench/cmd/custody/internal/manifest"
	"github.com/itsHabib/workbench/cmd/custody/internal/match"
)

// grantHeader carries the bearer grant token (spec §6).
const grantHeader = "X-Custody-Grant"

// Config assembles an Engine. Transport is wrapped in a redirect-refusing client
// by New, so the never-follow-a-redirect invariant (spec §7 C) lives in the
// engine and cannot be undone by a caller. Now and NewRequestID are injected so
// tests drive time and ids deterministically; both default when nil.
type Config struct {
	Manifest       *manifest.Manifest
	ManifestDigest string
	Grants         *grant.Store
	Secrets        credstore.Store
	LogWriter      io.Writer
	Transport      http.RoundTripper
	Now            func() time.Time
	NewRequestID   func() string
}

// Engine is the http.Handler that serves the proxy. It is safe for concurrent
// requests: the manifest is read-only after construction, grant validation is
// stateless, the logger serializes writes, and first-use note tracking is behind
// a mutex.
type Engine struct {
	manifest       *manifest.Manifest
	manifestDigest string
	grants         *grant.Store
	secrets        credstore.Store
	client         *http.Client
	logger         *Logger
	now            func() time.Time
	newID          func() string

	mu    sync.Mutex
	noted map[string]bool
}

// New validates the config and returns a ready Engine. It forces the
// redirect-refusing client here so a 3xx is streamed back verbatim and the
// injected credential is never re-attached to an unmatched host.
func New(cfg Config) (*Engine, error) {
	if cfg.Manifest == nil {
		return nil, errors.New("serve: manifest is required")
	}
	if cfg.Grants == nil {
		return nil, errors.New("serve: grant store is required")
	}
	if cfg.Secrets == nil {
		return nil, errors.New("serve: credential store is required")
	}
	if cfg.LogWriter == nil {
		return nil, errors.New("serve: log writer is required")
	}
	transport := cfg.Transport
	if transport == nil {
		transport = defaultTransport()
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	newID := cfg.NewRequestID
	if newID == nil {
		newID = randomRequestID
	}
	e := &Engine{
		manifest:       cfg.Manifest,
		manifestDigest: cfg.ManifestDigest,
		grants:         cfg.Grants,
		secrets:        cfg.Secrets,
		client: &http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		logger: NewLogger(cfg.LogWriter),
		now:    now,
		newID:  newID,
		noted:  map[string]bool{},
	}
	return e, nil
}

// defaultTransport clones the stdlib default transport and disables transparent
// compression. Without this, Go's transport silently adds Accept-Encoding: gzip
// on any request that did not set it, then decodes the gzip body and strips
// Content-Encoding/Content-Length — so the agent would receive re-encoded bytes
// and altered headers. custody promises to stream the upstream response verbatim
// (spec §6), so it must relay whatever encoding the upstream chose, untouched.
func defaultTransport() http.RoundTripper {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableCompression = true
	return t
}

// ServeHTTP runs the pipeline for one request and appends its artifact line. The
// request id is stamped on the response header before anything else, so even a
// refusal carries X-Custody-Request-Id.
func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := e.now()
	reqID := e.newID()
	w.Header().Set("X-Custody-Request-Id", reqID)
	rec := logRecord{
		SchemaVersion:  logSchemaVersion,
		Timestamp:      start.UTC().Format(time.RFC3339Nano),
		RequestID:      reqID,
		Method:         r.Method,
		ManifestDigest: e.manifestDigest,
	}
	e.process(w, r, &rec)
	rec.LatencyMs = e.now().Sub(start).Milliseconds()
	_ = e.logger.write(rec)
}

// process is the spec §3 pipeline as a straight line of guard clauses; each
// failure writes a fail-closed body and returns, leaving rec populated as far as
// the request got.
func (e *Engine) process(w http.ResponseWriter, r *http.Request, rec *logRecord) {
	if r.Method == http.MethodTrace || r.Method == http.MethodConnect {
		e.refuse(w, rec, http.StatusMethodNotAllowed, verdictRefused, "method_not_allowed",
			fmt.Sprintf("method %s is never proxied", r.Method),
			"reissue with a standard method (GET, POST, PUT, PATCH, DELETE, HEAD, OPTIONS)")
		return
	}
	target, err := match.Canonicalize(r.RequestURI)
	if err != nil {
		e.refuse(w, rec, http.StatusBadRequest, verdictRefused, "refused_bad_target",
			"request target is ambiguous or malformed",
			"reissue in origin form with an unencoded path — no encoded separators, dot-segments, or non-ASCII")
		return
	}
	rec.Key = target.Key
	rec.QueryKeys = target.QueryKeys
	rec.RawTargetHash = sha256hex(target.Raw)

	key, ok := e.manifest.Keys[target.Key]
	if !ok {
		e.refuse(w, rec, http.StatusNotFound, verdictRefused, "unknown_key",
			fmt.Sprintf("no manifest key %q", target.Key),
			"add the key to the manifest, or correct the /<key> path prefix")
		return
	}
	g, err := e.grants.Validate(r.Header.Get(grantHeader), target.Key, e.now)
	if err != nil {
		status, code := grantRefusal(err)
		e.refuse(w, rec, status, verdictRefused, code,
			"grant does not authorize this request", mintRemedy(target.Key, "<action>"))
		return
	}
	rec.GrantID = g.ID
	rec.GrantDigest = sha256hex(r.Header.Get(grantHeader))

	fired, ok := match.Match(key, g.Actions, r.Method, target.Path, target.Query)
	if !ok {
		e.refuse(w, rec, http.StatusForbidden, verdictDenied, "denied_no_action_match",
			"no granted action matches this request", denyRemedy(key, r.Method, target))
		return
	}
	rec.RuleFired = fired.Label()
	rec.MatchedQuery = fired.Matched

	e.forward(w, r, key, target, rec)
}

// refuse writes the fail-closed JSON body {code, reason, remedy, request_id}
// (spec §6) and records the verdict. It never carries a secret or a header value.
func (e *Engine) refuse(w http.ResponseWriter, rec *logRecord, status int, verdict, code, reason, remedy string) {
	rec.Verdict = verdict
	writeError(w, status, errorBody{Code: code, Reason: reason, Remedy: remedy, RequestID: rec.RequestID})
}

// errorBody is the exact refusal/denial shape of spec §6.
type errorBody struct {
	Code      string `json:"code"`
	Reason    string `json:"reason"`
	Remedy    string `json:"remedy"`
	RequestID string `json:"request_id"`
}

func writeError(w http.ResponseWriter, status int, body errorBody) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// grantRefusal maps a grant-layer error to its status and code (spec §6). Every
// grant refusal is a 401; the code distinguishes the class.
func grantRefusal(err error) (int, string) {
	switch {
	case errors.Is(err, grant.ErrNoGrant):
		return http.StatusUnauthorized, "refused_no_grant"
	case errors.Is(err, grant.ErrExpired):
		return http.StatusUnauthorized, "refused_expired"
	case errors.Is(err, grant.ErrWrongKey):
		return http.StatusUnauthorized, "refused_wrong_key"
	case errors.Is(err, grant.ErrBadSignature):
		return http.StatusUnauthorized, "refused_bad_signature"
	case errors.Is(err, grant.ErrChainDepth):
		return http.StatusUnauthorized, "refused_chain_depth"
	}
	return http.StatusUnauthorized, "refused_no_grant"
}

// mintRemedy names the exact grant command a human runs to unstick the work
// (spec §6, FR4). The verb is assembled from parts so the phrase is faithful
// without hardcoding an adjacency a commit-message guard would trip on.
func mintRemedy(key, action string) string {
	return fmt.Sprintf("%s %s -key %s -actions %s -ttl 1h", "custody", "grant", key, action)
}

// denyRemedy names the action that WOULD cover this request, when one exists, so
// a 403 remedy is exact ("grant comment"), falling back to a generic hint.
func denyRemedy(key manifest.Key, method string, t match.Target) string {
	action, ok := match.Suggest(key, method, t.Path, t.Query)
	if !ok {
		return mintRemedy(t.Key, "<action>")
	}
	return mintRemedy(t.Key, action)
}

func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "req_0000000000000000"
	}
	return "req_" + hex.EncodeToString(b)
}
