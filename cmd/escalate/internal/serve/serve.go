// Package serve is the HTTP transport adapter for the resolution back-channel:
// it turns a Slack interactive-action callback into an ingest.Decision and
// drives the SAME ingest.Client the `resolve` verb uses. It adds no decision
// logic — the mechanism (validate → shell `gate resolve`) is unchanged; only
// the SOURCE of the decision differs (a signed HTTP callback instead of CLI
// flags). It is the second transport over one contract that lets a Slack button
// and the CLI both drive one ingest.Decision.
//
// It is a security surface, so authentication is the first thing the handler
// does and the last thing it trusts: every request is rejected unless it carries
// a valid Slack signature over its raw body within a fresh timestamp window, and
// the `who` recorded on the resolution is derived from the VERIFIED Slack
// identity in the signed payload — never from a field a client could assert.
// The endpoint can only ever drive `gate resolve` for an already-parked
// escalation under an already-live grant, so the whole blast radius is
// "approve/block a PR gate already parked for judgment" — nothing else.
package serve

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/itsHabib/workbench/cmd/escalate/internal/ingest"
	"github.com/itsHabib/workbench/contracts/escalation"
)

const (
	// hdrSig / hdrTS are Slack's signature headers. The signature is
	// v0=hex(HMAC-SHA256(secret, "v0:"+timestamp+":"+rawBody)); the timestamp is
	// the epoch second Slack signed at, and it is part of the signed base string
	// so it cannot be replayed under a different clock.
	hdrSig = "X-Slack-Signature"
	hdrTS  = "X-Slack-Request-Timestamp"

	// actionApprove / actionBlock are the two button action_ids flare renders.
	// The action_id — not a client-chosen field — selects the verdict, so a tap
	// maps to exactly one of gate's decisions.
	actionApprove = "approve"
	actionBlock   = "block"

	// maxSkew bounds how old a signed request may be. Slack recommends five
	// minutes; anything staler is rejected before parsing, so a captured request
	// cannot be replayed hours later.
	maxSkew = 5 * time.Minute

	// maxBody caps the request body a single Slack callback can carry, so a
	// hostile client cannot exhaust memory before the signature is even checked.
	maxBody = 1 << 20

	// codeGateError is gate's hard-error exit (a state read/write failure, not a
	// decision). Unlike gate's decision codes 0–3, it means no decision landed,
	// so serve reports it as an HTTP failure — Slack must retry, not treat the
	// callback as acknowledged.
	codeGateError = 4

	// resolveTimeout bounds the detached gate resolve so a hung subprocess cannot
	// pin a goroutine forever. It sits under main's 30s WriteTimeout so the
	// handler still has room to write gate's outcome after resolve returns.
	resolveTimeout = 25 * time.Second
)

// GrantFinder resolves a parked escalation id to the grant its run parked under.
// It is the seam that lets `serve` read the grant from the parked escalation —
// never from the client payload — so an approval always runs under the grant the
// run actually parked with. The default implementation shells `gate next -json`
// (the console read seam), keeping the boundary law: serve reads gate's output,
// it never imports gate.
type GrantFinder func(ctx context.Context, escID string) (string, error)

// Config assembles a Server. Secret and Ingest are required; a nil Now uses
// time.Now. FindGrant is required in production but injectable for tests.
type Config struct {
	Secret    []byte
	Ingest    *ingest.Client
	FindGrant GrantFinder
	Now       func() time.Time
}

// Server is the Slack callback ingress. It implements http.Handler, so a caller
// wires it straight into http.ListenAndServe. Construct it with New; its zero
// value is not useful (it needs a signing secret and an ingest client).
type Server struct {
	secret    []byte
	ingest    *ingest.Client
	findGrant GrantFinder
	now       func() time.Time
	locks     *escLocks
}

// New builds a Server from cfg. A nil Now falls back to time.Now.
func New(cfg Config) *Server {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Server{
		secret:    cfg.Secret,
		ingest:    cfg.Ingest,
		findGrant: cfg.FindGrant,
		now:       now,
		locks:     &escLocks{m: make(map[string]*sync.Mutex)},
	}
}

// escLocks serializes callbacks per escalation id WITHIN this process. The HTTP
// transport serves each callback in its own goroutine, so two taps for the SAME
// escalation — a double-tap, or a Slack retry racing the first — can arrive
// concurrently. Gate's open-check and its terminal append are not one atomic
// step, so without this both could pass the open-check before either records an
// action and both would resolve one park. This holds one lock per escalation
// across the whole lookup→resolve, so the second waits and then finds the park
// already closed (refused, not double-applied). Different escalations never
// contend. Entries are not reclaimed: they are bounded by the escalations a run
// ever parks, so the growth is negligible for this single-process ingress.
//
// SCOPE: this covers the Phase-1 deployment — ONE serve process behind one
// tunnel. It does NOT serialize a second serve process on the same -state, nor a
// CLI `escalate resolve` racing an HTTP callback; that race predates serve (two
// parallel CLI resolves collide identically) and its durable fix is an atomic
// compare-and-resolve in gate, tracked in FOLLOWUPS.md.
type escLocks struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}

// lock acquires the per-id mutex and returns its release. The outer mutex guards
// only the tiny map lookup; the per-id mutex is what callers actually hold.
func (k *escLocks) lock(id string) func() {
	k.mu.Lock()
	l, ok := k.m[id]
	if !ok {
		l = new(sync.Mutex)
		k.m[id] = l
	}
	k.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// ServeHTTP handles one Slack interactive-action callback. The order is the
// security contract: read the raw body, VERIFY THE SIGNATURE before parsing
// anything, then map the verified payload to a decision, read the grant from the
// parked escalation, and drive the ingest client. Any failure short-circuits
// with a status and never reaches gate — an unsigned or stale request is refused
// before a single field is trusted.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	if err := verify(s.secret, r.Header.Get(hdrSig), r.Header.Get(hdrTS), body, s.now()); err != nil {
		http.Error(w, "signature verification failed", http.StatusUnauthorized)
		return
	}
	d, err := decisionFromPayload(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Serialize everything below per escalation id: the lookup and resolve for one
	// escalation run under one lock, so concurrent taps for it can't both act.
	defer s.locks.lock(d.Escalation)()
	// Detach the authoritative write from the request lifecycle. The tap is
	// authenticated and we are committed to acting on it, so a client / Slack /
	// tunnel disconnect must NOT cancel r.Context() and kill `gate resolve`
	// mid-append — gate writes the judgment, verdict, action, and resolution
	// separately, and a half-written transaction (a judgment with no terminal
	// action) would let a retry double-apply. A bounded independent context caps
	// a hung gate without letting the transport abort a decision in flight.
	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()
	grant, err := s.findGrant(ctx, d.Escalation)
	if errors.Is(err, ErrNotParked) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "grant lookup: "+err.Error(), http.StatusBadGateway)
		return
	}
	d.Grant = grant
	out, code, err := s.ingest.Resolve(ctx, d)
	if err != nil {
		http.Error(w, "resolve: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeOutcome(w, out, code)
}

// verify is the authentication gate. It rejects a request that is missing
// headers, carries a stale or unparseable timestamp, or whose HMAC over
// "v0:{ts}:{body}" does not match — using a constant-time compare so a mismatch
// leaks no timing. It runs on the RAW body before any parse, so an attacker
// never reaches the decision path. This is policy (what makes a callback
// trustworthy), kept separate from the transport plumbing above.
func verify(secret []byte, sig, ts string, body []byte, now time.Time) error {
	if len(secret) == 0 {
		return errors.New("serve: no signing secret configured")
	}
	if sig == "" || ts == "" {
		return errors.New("serve: missing slack signature headers")
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("serve: bad timestamp %q: %w", ts, err)
	}
	if skew := now.Sub(time.Unix(sec, 0)); skew > maxSkew || skew < -maxSkew {
		return fmt.Errorf("serve: stale timestamp: skew %s exceeds %s", skew, maxSkew)
	}
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "v0:%s:%s", ts, body)
	want := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return errors.New("serve: signature mismatch")
	}
	return nil
}

// slackPayload is the slice of a Slack interactive-action callback the
// back-channel reads: who acted (the identity Slack verified when it signed the
// request) and which button they tapped (the action_id selects the verdict, the
// value carries the escalation id). A deliberate small copy of Slack's shape
// rather than a dependency — serve only ever reads these fields.
type slackPayload struct {
	Type string `json:"type"`
	User struct {
		ID       string `json:"id"`
		Username string `json:"username"`
		Name     string `json:"name"`
	} `json:"user"`
	Actions []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	} `json:"actions"`
}

// decisionFromPayload maps a verified callback body to a Decision, minus the
// grant (the handler reads that from the parked escalation). Slack posts the
// callback as application/x-www-form-urlencoded with a single `payload` field
// holding URL-encoded JSON. `who` and the verdict come only from the payload's
// own identity and action_id — there is no client-settable who to honor, and a
// stray `who` key in the JSON is ignored by construction.
func decisionFromPayload(body []byte) (ingest.Decision, error) {
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return ingest.Decision{}, fmt.Errorf("serve: parse form: %w", err)
	}
	raw := form.Get("payload")
	if raw == "" {
		return ingest.Decision{}, errors.New("serve: no payload field")
	}
	var p slackPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return ingest.Decision{}, fmt.Errorf("serve: decode payload: %w", err)
	}
	if len(p.Actions) == 0 {
		return ingest.Decision{}, errors.New("serve: payload carries no action")
	}
	verdict, err := verdictFor(p.Actions[0].ActionID)
	if err != nil {
		return ingest.Decision{}, err
	}
	who := slackWho(p.User.Username, p.User.Name, p.User.ID)
	if who == "" {
		return ingest.Decision{}, errors.New("serve: no verified slack identity in payload")
	}
	return ingest.Decision{
		Escalation: p.Actions[0].Value,
		Verdict:    verdict,
		Who:        who,
		Why:        fmt.Sprintf("%s in Slack by %s", verdictWord(verdict), who),
	}, nil
}

// verdictFor maps a button's action_id to gate's decision vocabulary. An unknown
// action_id is rejected rather than defaulted, so a malformed or unexpected
// button never silently approves or blocks.
func verdictFor(actionID string) (string, error) {
	switch actionID {
	case actionApprove:
		return escalation.DecisionPass, nil
	case actionBlock:
		return escalation.DecisionBlock, nil
	}
	return "", fmt.Errorf("serve: unknown action_id %q", actionID)
}

// verdictWord renders a verdict for the human-facing why line.
func verdictWord(verdict string) string {
	if verdict == escalation.DecisionBlock {
		return "blocked"
	}
	return "approved"
}

// slackWho renders the verified identity for the resolution's `who`, keeping
// BOTH the immutable Slack user id and the human handle when it has them:
// "@handle (Uxxxx)". The id is the only stable, unique identifier — a handle can
// be renamed or shared — so it must be recorded, not just used as a fallback;
// the handle is kept because a bare id is unreadable in an audit. It is the ONLY
// source of `who`: the resolution records who Slack said tapped the button, not
// who a payload claimed to be.
func slackWho(username, name, id string) string {
	handle := username
	if handle == "" {
		handle = name
	}
	if handle == "" {
		return id
	}
	if id == "" {
		return "@" + handle
	}
	return "@" + handle + " (" + id + ")"
}

// writeOutcome relays gate's result once gate has actually run: gate's JSON body
// plus its exit code. The HTTP status splits on whether a DECISION LANDED. Gate's
// decision codes 0–3 (merge / block / re-park / refuse) are landed decisions, so
// they are 200 — the callback was ingested and driven, the outcome is DATA in the
// body, and Slack must NOT retry a decision that already happened. Gate's hard
// error (code 4) is NOT a decision — the resolution may never have been recorded
// — so it is a 502: Slack should retry rather than treat the tap as acknowledged.
// Ingress-level failures (bad signature, bad payload, a park that is gone, gate
// unrunnable) get their own 4xx/5xx above and never reach here.
func writeOutcome(w http.ResponseWriter, out []byte, code int) {
	w.Header().Set("Content-Type", "application/json")
	if code == codeGateError {
		w.WriteHeader(http.StatusBadGateway)
	}
	if len(out) == 0 {
		fmt.Fprintf(w, `{"exit_code":%d}`, code)
		return
	}
	w.Write(out)
}
