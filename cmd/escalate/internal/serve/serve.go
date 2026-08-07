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
//
// Slack requires an interactive callback be acknowledged within ~3s, but the
// authoritative work (grant lookup + `gate resolve`, which shells `gate` and may
// shell `gh`) can exceed that. So the handler splits: it verifies + authorizes
// synchronously, ACKS immediately, and runs the resolve in the background,
// delivering the final outcome to the interaction's `response_url`. The security
// gate stays on the synchronous path — nothing an unauthenticated or
// unauthorized caller sends ever reaches the background.
package serve

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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

	// maxSkew bounds how old a signed request may be. Slack recommends five
	// minutes; anything staler is rejected before parsing, so a captured request
	// cannot be replayed hours later.
	maxSkew = 5 * time.Minute

	// maxBody caps the request body a single Slack callback can carry, so a
	// hostile client cannot exhaust memory before the signature is even checked.
	maxBody = 1 << 20

	// resolveTimeout bounds the detached gate resolve so a hung subprocess cannot
	// pin a background goroutine forever. It is independent of the HTTP request
	// (which has already been acked), so the transport can never abort a decision
	// in flight, and a wedged gate is still capped.
	resolveTimeout = 25 * time.Second

	// deliverTimeout bounds the outcome POST to response_url. It is a FRESH budget,
	// started after the resolve returns — never the resolve's leftover deadline, or
	// a resolve that ran near resolveTimeout would leave the card stuck on the ack
	// with no outcome ever delivered.
	deliverTimeout = 10 * time.Second

	// gate's resolve exit codes, its decision contract passed through faithfully:
	// 0 merge / 1 blocked / 2 parked / 3 refused. Anything else (notably 4, gate's
	// hard error) means no clean decision landed.
	codeMerge   = 0
	codeBlocked = 1
	codeParked  = 2
	codeRefused = 3
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
// Authorize is the authorization gate (below); a nil Authorize is fail-closed —
// it denies every caller — so a Server built without one resolves nothing.
type Config struct {
	Secret    []byte
	Ingest    *ingest.Client
	FindGrant GrantFinder
	// Authorize reports whether the VERIFIED Slack user id may resolve a park. It
	// runs after signature verification: the signature proves Slack sent the
	// callback, this proves the human behind it is allowed to act. Without it any
	// member of the escalation channel could tap Approve/Block and move a live
	// merge gate — authentication is not authorization. A nil Authorize denies
	// everyone (fail-closed); build one with AllowUsers.
	Authorize func(slackUserID string) bool
	Now       func() time.Time
}

// Server is the Slack callback ingress. It implements http.Handler, so a caller
// wires it straight into http.ListenAndServe. Construct it with New; its zero
// value is not useful (it needs a signing secret, an ingest client, and an
// authorizer).
type Server struct {
	secret    []byte
	ingest    *ingest.Client
	findGrant GrantFinder
	authorize func(string) bool
	now       func() time.Time
	locks     *escLocks
	// post ships the rendered outcome card to the interaction's response_url. It
	// is a white-box test seam: production uses postResponse (the https,
	// Slack-host-guarded POST); a test overrides it to capture the card without a
	// real Slack endpoint.
	post func(ctx context.Context, responseURL string, body []byte) error
	// log carries the background goroutine's diagnostics — once the tap is acked,
	// a resolve or delivery failure has no HTTP status left to ride, so it goes to
	// the log (and, when it can, to the Slack card).
	log *log.Logger
	// inflight tracks the accepted-but-unfinished background resolves so a caller
	// can DRAIN them on graceful shutdown (Wait). Since the ack removes the card's
	// buttons and Slack won't retry a 200, a redeploy or SIGTERM that dropped an
	// in-flight resolve would silently lose the decision; draining closes that.
	inflight sync.WaitGroup
}

// New builds a Server from cfg. A nil Now falls back to time.Now; a nil Authorize
// falls back to deny-all, so authorization must be configured explicitly — an
// unset allowlist never silently accepts everyone.
func New(cfg Config) *Server {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	authorize := cfg.Authorize
	if authorize == nil {
		authorize = func(string) bool { return false }
	}
	return &Server{
		secret:    cfg.Secret,
		ingest:    cfg.Ingest,
		findGrant: cfg.FindGrant,
		authorize: authorize,
		now:       now,
		locks:     &escLocks{m: make(map[string]*sync.Mutex)},
		post:      postResponse,
		log:       log.Default(),
	}
}

// AllowUsers builds an authorizer that admits exactly the given Slack user ids —
// the immutable `Uxxxx` ids, never handles (a handle can be renamed onto another
// account). An empty list admits no one, so a misconfigured allowlist fails
// closed rather than open. This is the authorization the ingress needs before a
// channel-wide button is safe: only the listed operators can resolve a park.
func AllowUsers(ids ...string) func(string) bool {
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" {
			allowed[id] = true
		}
	}
	return func(id string) bool { return id != "" && allowed[id] }
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
// anything, map the verified payload to a callback, and authorize the verified
// user — all synchronously, because these are the only failures a client can
// still be told about over this request. Then it ACKS within Slack's ~3s window
// and runs the authoritative resolve in the background (process), which delivers
// the real outcome to the interaction's response_url. An unsigned, malformed, or
// unauthorized request short-circuits with a status and never reaches gate.
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
	cb, err := callbackFromPayload(body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Authorization, after authentication: the signature proved Slack sent this,
	// now prove the verified human may act. A channel member who is not an
	// authorized operator is refused here — before the ack, before any lock,
	// lookup, or resolve — so a button visible to a whole channel still resolves
	// only for the allowlist.
	if !s.authorize(cb.userID) {
		http.Error(w, "not authorized to resolve escalations", http.StatusForbidden)
		return
	}
	// The tap is authenticated and authorized and we are committed to acting on
	// it. Ack now (Slack's ~3s window) so the tap reads as received, then run the
	// resolve — which can exceed 3s — off the request path and report its outcome
	// to response_url. The ack replaces the card's buttons so the tap can't be
	// repeated while the resolve is in flight. Count the work in before spawning so
	// a graceful shutdown drains it (Wait) rather than dropping the acked decision.
	s.inflight.Add(1)
	writeAck(w, cb)
	go s.process(cb)
}

// Wait blocks until every accepted resolution's background work has finished. A
// caller drains it during graceful shutdown — after the HTTP server has stopped
// accepting — so a redeploy or SIGTERM doesn't drop an acked-but-unrecorded tap.
// It is bounded in practice: each resolve is capped by resolveTimeout and each
// delivery by deliverTimeout.
func (s *Server) Wait() { s.inflight.Wait() }

// process runs the authoritative resolution off the request path, after the ack.
// It holds the per-escalation lock across the whole lookup→resolve (so concurrent
// taps for one escalation can't both act) and bounds the work with an independent
// context — a client / Slack / tunnel disconnect must NOT cancel `gate resolve`
// mid-append, since gate writes the judgment, verdict, action, and resolution
// separately and a half-written transaction would let a retry double-apply.
// Delivery runs under a FRESH context so a resolve that used most of resolveTimeout
// can still post its outcome. A panic in one resolve is contained to that resolve
// (recovered + logged), not allowed to take down the whole ingress. Its only
// channels back to the operator are the Slack card (deliver) and the log.
func (s *Server) process(cb callback) {
	defer s.inflight.Done()
	defer func() {
		if r := recover(); r != nil {
			s.log.Printf("escalate serve: resolve %s panicked: %v", cb.decision.Escalation, r)
		}
	}()
	defer s.locks.lock(cb.decision.Escalation)()

	ctx, cancel := context.WithTimeout(context.Background(), resolveTimeout)
	defer cancel()
	code, err := s.resolve(ctx, cb.decision)
	if err != nil {
		s.log.Printf("escalate serve: resolve %s: %v", cb.decision.Escalation, err)
	}

	dctx, dcancel := context.WithTimeout(context.Background(), deliverTimeout)
	defer dcancel()
	slackUpdated := s.deliver(dctx, cb, code, err)
	if err != nil {
		return
	}
	if code >= codeMerge && code <= codeRefused {
		s.log.Printf("escalate serve: resolved escalation=%s gate_exit=%d slack_updated=%t", cb.decision.Escalation, code, slackUpdated)
		return
	}
	s.log.Printf("escalate serve: failed escalation=%s gate_exit=%d slack_updated=%t", cb.decision.Escalation, code, slackUpdated)
}

// resolve reads the grant from the parked escalation and drives the ingest
// client — the same mechanism the CLI resolve verb uses. A grant-lookup failure
// (including ErrNotParked for an already-resolved park) short-circuits before any
// resolve, so a replayed tap records nothing. It returns gate's exit code, which
// deliver maps to the human outcome.
func (s *Server) resolve(ctx context.Context, d ingest.Decision) (int, error) {
	grant, err := s.findGrant(ctx, d.Escalation)
	if err != nil {
		return 0, err
	}
	d.Grant = grant
	_, code, err := s.ingest.Resolve(ctx, d)
	return code, err
}

// deliver renders the outcome as a Slack card and posts it to the interaction's
// response_url, replacing the working ack with the final "✅ merged / ⛔ blocked /
// ☑️ already resolved" state. A missing response_url (a CLI-shaped or malformed
// callback) or a post failure is logged, not fatal — the decision already landed
// in gate; only the card update is best-effort.
func (s *Server) deliver(ctx context.Context, cb callback, code int, err error) bool {
	if cb.responseURL == "" {
		s.log.Printf("escalate serve: resolve %s: no response_url, outcome not delivered to Slack", cb.decision.Escalation)
		return false
	}
	card, merr := outcomeCard(cb, code, err)
	if merr != nil {
		s.log.Printf("escalate serve: render outcome for %s: %v", cb.decision.Escalation, merr)
		return false
	}
	if perr := s.post(ctx, cb.responseURL, card); perr != nil {
		s.log.Printf("escalate serve: deliver outcome for %s: %v", cb.decision.Escalation, perr)
		return false
	}
	return true
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
// request), which button they tapped (the action_id selects the verdict, the
// value carries the escalation id), and the response_url — the per-interaction
// webhook serve posts the outcome back to. A deliberate small copy of Slack's
// shape rather than a dependency — serve only ever reads these fields.
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
	ResponseURL string `json:"response_url"`
}

// callback is a parsed, verified Slack interaction serve acts on: the decision to
// drive, the verified Slack user id it was authorized on, and the interaction's
// response_url (the outcome webhook). It bundles what the synchronous handler
// derives and the background process consumes, so neither passes a widening tuple.
type callback struct {
	decision    ingest.Decision
	userID      string
	responseURL string
}

// callbackFromPayload maps a verified callback body to the decision to drive, the
// verified Slack user id the handler authorizes on, and the interaction's
// response_url. Slack posts the callback as application/x-www-form-urlencoded
// with a single `payload` field holding URL-encoded JSON. `who`, the verdict, and
// the returned id come only from the payload's own identity and action_id — there
// is no client-settable who to honor, and a stray `who` key in the JSON is
// ignored by construction.
func callbackFromPayload(body []byte) (callback, error) {
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return callback{}, fmt.Errorf("serve: parse form: %w", err)
	}
	raw := form.Get("payload")
	if raw == "" {
		return callback{}, errors.New("serve: no payload field")
	}
	var p slackPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return callback{}, fmt.Errorf("serve: decode payload: %w", err)
	}
	if len(p.Actions) == 0 {
		return callback{}, errors.New("serve: payload carries no action")
	}
	verdict, err := verdictFor(p.Actions[0].ActionID)
	if err != nil {
		return callback{}, err
	}
	who := slackWho(p.User.Username, p.User.Name, p.User.ID)
	if who == "" {
		return callback{}, errors.New("serve: no verified slack identity in payload")
	}
	return callback{
		decision: ingest.Decision{
			Escalation: p.Actions[0].Value,
			Verdict:    verdict,
			Who:        who,
			Why:        fmt.Sprintf("%s in Slack by %s", verdictWord(verdict), who),
		},
		userID:      p.User.ID,
		responseURL: p.ResponseURL,
	}, nil
}

// verdictFor maps a button's action_id to gate's decision vocabulary. The
// action_id vocabulary is the shared contract flare renders and serve parses
// (escalation.ActionApprove / ActionBlock); the mapping to a decision is serve's
// policy. An unknown action_id is rejected rather than defaulted, so a malformed
// or unexpected button never silently approves or blocks.
func verdictFor(actionID string) (string, error) {
	switch actionID {
	case escalation.ActionApprove:
		return escalation.DecisionPass, nil
	case escalation.ActionBlock:
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

// responseMessage is the slice of Slack's message-update shape serve writes: the
// text to show and replace_original, which swaps the source card (dropping its
// now-stale buttons) rather than posting a new message. Used for both the ack and
// the final outcome — the outcome replaces the ack, which replaced the buttons.
type responseMessage struct {
	ReplaceOriginal bool   `json:"replace_original"`
	Text            string `json:"text"`
}

// writeAck answers the interaction inside Slack's ~3s window: it replaces the
// card with a working state — dropping the Approve/Block buttons so the tap can't
// be repeated while the resolve runs — and returns 200. process replaces this
// again via response_url once the real outcome lands.
func writeAck(w http.ResponseWriter, cb callback) {
	w.Header().Set("Content-Type", "application/json")
	msg := responseMessage{
		ReplaceOriginal: true,
		Text:            fmt.Sprintf("⏳ Recording %s's decision…", cb.decision.Who),
	}
	// The ack body is best-effort presentation; the decision is already committed
	// to the background, so a failed encode changes the card, not the outcome.
	_ = json.NewEncoder(w).Encode(msg)
}

// outcomeCard renders the resolution result as a Slack replace_original message.
// It is policy — the vocabulary the operator reads on their phone — kept out of
// the transport (post) that ships it.
func outcomeCard(cb callback, code int, err error) ([]byte, error) {
	return json.Marshal(responseMessage{ReplaceOriginal: true, Text: outcomeText(cb, code, err)})
}

// outcomeText maps gate's exit code — and the ingest-side errors that
// short-circuit before gate — to the one-line outcome shown on the card.
// ErrGrantlessPark is checked before its wrapper target ErrNotParked: the park
// is still there, so "already resolved" would be exactly the misleading status
// the distinct error exists to avoid. ErrNotParked is the expected replay
// outcome (the park is already gone), so it reads as a benign "already
// resolved", not a failure. Any exit code outside gate's 0–3 decision space
// (notably 4, its hard error) means no clean decision landed, so the card says
// the tap may need retrying.
func outcomeText(cb callback, code int, err error) string {
	who := cb.decision.Who
	if errors.Is(err, ErrGrantlessPark) {
		return "↗️ This park carries no gate grant — it resolves out-of-band, in the producer's own flow."
	}
	if errors.Is(err, ErrNotParked) {
		return "☑️ Already resolved — this escalation is no longer parked."
	}
	if err != nil {
		return fmt.Sprintf("❌ Could not record %s's decision — check the serve log.", who)
	}
	switch code {
	case codeMerge:
		return fmt.Sprintf("✅ Approved by %s — gate authorized the merge.", who)
	case codeBlocked:
		return fmt.Sprintf("⛔ Blocked by %s.", who)
	case codeParked:
		return fmt.Sprintf("🔁 Re-parked for judgment (%s).", who)
	case codeRefused:
		return fmt.Sprintf("🚫 Refused (%s).", who)
	}
	return fmt.Sprintf("⚠️ Gate error — %s's decision may not have landed; retry the tap.", who)
}

// postResponse is the default outcome transport: it POSTs the rendered card JSON
// to the interaction's response_url. Mechanism only — it ships bytes and reports
// a transport failure; what to say is decided above. response_url is a
// Slack-issued per-interaction webhook, so it is guarded to https on a Slack host
// (checkSlackURL) even though it rides a signature-verified payload — defense in
// depth so a serve process can never be aimed at an internal address.
func postResponse(ctx context.Context, responseURL string, body []byte) error {
	if err := checkSlackURL(responseURL); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, responseURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("serve: response_url returned %s", resp.Status)
	}
	return nil
}

// checkSlackURL bounds where postResponse may POST: an https URL on a slack.com
// host. response_url is authenticated (it rides the signed callback), so this is
// defense in depth — it keeps a forged-but-somehow-signed payload from turning
// the ingress into an SSRF against an internal service.
func checkSlackURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("serve: bad response_url: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("serve: response_url must be https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host != "slack.com" && !strings.HasSuffix(host, ".slack.com") {
		return fmt.Errorf("serve: response_url host %q is not slack.com", host)
	}
	return nil
}
