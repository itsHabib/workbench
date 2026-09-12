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
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/escalate/internal/ingest"
	"github.com/itsHabib/workbench/contracts"
	"github.com/itsHabib/workbench/contracts/escalation"
	"github.com/itsHabib/workbench/contracts/grantrequest"
)

var testSecret = []byte("8f742231b10c8537228d4e5a1a1a2d3f")

// fixedNow anchors the timestamp window so signing and verification agree on
// "now" without touching the wall clock.
var fixedNow = time.Unix(1_700_000_000, 0)

// testResponseURL is a Slack-shaped response_url the fixtures carry, so the async
// deliver path runs. The sink installed by withSink captures the card, so the
// value is never actually POSTed — its host is only exercised by TestCheckSlackURL.
const testResponseURL = "https://hooks.slack.com/actions/T01/B02/xyz"

func sign(secret []byte, ts string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "v0:%s:%s", ts, body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}

// formBody wraps a Slack payload JSON as the application/x-www-form-urlencoded
// body Slack actually posts: a single `payload` field.
func formBody(payloadJSON string) []byte {
	return []byte(url.Values{"payload": {payloadJSON}}.Encode())
}

func payloadJSON(actionID, value, username string) string {
	return fmt.Sprintf(
		`{"type":"block_actions","user":{"id":"U1","username":%q},"response_url":%q,"actions":[{"action_id":%q,"value":%q}]}`,
		username, testResponseURL, actionID, value,
	)
}

// payloadJSONNoURL omits response_url — the shape a CLI-built or malformed
// callback carries, where the outcome cannot be delivered to a Slack card.
func payloadJSONNoURL(actionID, value, username string) string {
	return fmt.Sprintf(
		`{"type":"block_actions","user":{"id":"U1","username":%q},"actions":[{"action_id":%q,"value":%q}]}`,
		username, actionID, value,
	)
}

// signedRequest builds a POST signed as Slack would sign it at instant `at`.
func signedRequest(secret []byte, at time.Time, body []byte) *http.Request {
	ts := strconv.FormatInt(at.Unix(), 10)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set(hdrTS, ts)
	req.Header.Set(hdrSig, sign(secret, ts, body))
	return req
}

// captured records the argv each Resolve shelled and returns a canned outcome,
// so the whole mapping is asserted without a real gate binary.
type captured struct {
	calls [][]string
	code  int
	out   []byte
}

func (c *captured) runner(_ context.Context, _ string, args ...string) ([]byte, int, error) {
	c.calls = append(c.calls, args)
	return c.out, c.code, nil
}

// deliverSink stands in for the real response_url POST (postResponse): it records
// every outcome card serve delivers and signals `done` for each, so a test can
// wait for the ASYNC resolve+delivery to finish before asserting. It is installed
// by withSink, replacing the https/Slack-host transport with an in-memory capture.
type deliverSink struct {
	mu    sync.Mutex
	cards [][]byte
	urls  []string
	done  chan struct{}
}

func newDeliverSink() *deliverSink { return &deliverSink{done: make(chan struct{}, 64)} }

func (d *deliverSink) deliver(_ context.Context, respURL string, body []byte) error {
	d.mu.Lock()
	d.cards = append(d.cards, body)
	d.urls = append(d.urls, respURL)
	d.mu.Unlock()
	d.done <- struct{}{}
	return nil
}

// wait blocks until n outcomes have been delivered — the synchronization every
// async test leans on: after wait returns, the resolve for each is complete and
// its recorded argv is safe to read (the channel send happens-after the resolve).
func (d *deliverSink) wait(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-d.done:
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for outcome delivery %d of %d", i+1, n)
		}
	}
}

// texts renders each delivered card's text, so a test can assert the human
// outcome vocabulary the operator sees.
func (d *deliverSink) texts() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, 0, len(d.cards))
	for _, c := range d.cards {
		var m responseMessage
		_ = json.Unmarshal(c, &m)
		out = append(out, m.Text)
	}
	return out
}

// withSink replaces a server's outcome transport with an in-memory sink and
// silences its background log, so async tests capture the card without a real
// Slack endpoint and don't spew to stderr.
func withSink(s *Server) *deliverSink {
	sink := newDeliverSink()
	s.post = sink.deliver
	s.log = log.New(io.Discard, "", 0)
	return sink
}

// allowAll authorizes every user; the shared constructor uses it so tests that
// exercise the authn / mapping / replay paths are not entangled with the authz
// gate, which TestServeHTTPAuthorizesVerifiedUser covers on its own.
func allowAll(string) bool { return true }

func newServer(cr *captured, findGrant GrantFinder) (*Server, *deliverSink) {
	s := New(Config{
		Secret:    testSecret,
		Ingest:    ingest.New("gate", "", cr.runner),
		FindGrant: findGrant,
		Authorize: allowAll,
		Now:       func() time.Time { return fixedNow },
	})
	return s, withSink(s)
}

func fixedGrant(grant string, calls *int) GrantFinder {
	return func(_ context.Context, _ string) (string, error) {
		*calls++
		return grant, nil
	}
}

// TestVerify pins the authentication gate: a correctly signed, fresh request
// passes; a tampered signature, a stale timestamp, missing headers, and an
// unconfigured secret each fail — before any body is parsed.
func TestVerify(t *testing.T) {
	body := formBody(payloadJSON(escalation.ActionApprove, "esc_a", "michael"))
	ts := strconv.FormatInt(fixedNow.Unix(), 10)
	good := sign(testSecret, ts, body)

	cases := []struct {
		name    string
		secret  []byte
		sig     string
		ts      string
		wantErr bool
	}{
		{"valid", testSecret, good, ts, false},
		{"tampered signature", testSecret, "v0=deadbeef", ts, true},
		{"wrong secret", []byte("nope"), good, ts, true},
		{"missing signature", testSecret, "", ts, true},
		{"missing timestamp", testSecret, good, "", true},
		{"unparseable timestamp", testSecret, good, "not-a-number", true},
		{"no secret configured", nil, good, ts, true},
		{"stale timestamp", testSecret, good, strconv.FormatInt(fixedNow.Add(-6*time.Minute).Unix(), 10), true},
		{"future timestamp", testSecret, good, strconv.FormatInt(fixedNow.Add(6*time.Minute).Unix(), 10), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A skewed timestamp changes the signed base string, so re-sign it
			// with the case's own timestamp — otherwise the stale cases would
			// fail on the signature, not the window they mean to exercise.
			sig := tc.sig
			if tc.ts != ts && tc.ts != "" && tc.ts != "not-a-number" {
				sig = sign(testSecret, tc.ts, body)
			}
			err := verify(tc.secret, sig, tc.ts, body, fixedNow)
			if tc.wantErr && err == nil {
				t.Fatalf("want error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want nil, got %v", err)
			}
		})
	}
}

// TestServeHTTPMapsPayloadToDecision is the end-to-end mapping proof: a signed
// Approve callback acks 200, then — asynchronously — drives `gate resolve` with
// the verdict, escalation id, grant, who, and why derived from the request, and
// delivers an approved-merge card to the interaction's response_url.
func TestServeHTTPMapsPayloadToDecision(t *testing.T) {
	cr := &captured{out: []byte(`{"outcome":"would_merge"}`), code: 0}
	grantCalls := 0
	srv, sink := newServer(cr, fixedGrant("grt_live", &grantCalls))

	body := formBody(payloadJSON(escalation.ActionApprove, "esc_abc", "michael"))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, signedRequest(testSecret, fixedNow, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("ack status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	sink.wait(t, 1)
	if grantCalls != 1 {
		t.Fatalf("grant finder called %d times, want 1", grantCalls)
	}
	if len(cr.calls) != 1 {
		t.Fatalf("resolve called %d times, want 1", len(cr.calls))
	}
	want := []string{
		"resolve",
		"-escalation", "esc_abc",
		"-grant", "grt_live",
		"-decision", "pass",
		"-why", "approved in Slack by @michael (U1)",
		"-who", "@michael (U1)",
		"-method", contracts.MethodSlackInteractive,
	}
	if !reflect.DeepEqual(cr.calls[0], want) {
		t.Fatalf("argv mismatch:\n got=%v\nwant=%v", cr.calls[0], want)
	}
	if got := sink.texts()[0]; !strings.Contains(got, "Approved by @michael (U1)") {
		t.Fatalf("outcome card = %q, want an approved-merge card", got)
	}
}

func TestServeHTTPForwardsOriginalSignedGrantCallbackToGate(t *testing.T) {
	var capturedBody []byte
	var capturedSignature, capturedTimestamp string
	grantTap := func(_ context.Context, body []byte, signature, timestamp string) ([]byte, int, error) {
		capturedBody = append([]byte(nil), body...)
		capturedSignature = signature
		capturedTimestamp = timestamp
		return []byte(`{"outcome":"granted","repo":"o/r","pr":7,"head_sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`), codeMerge, nil
	}
	grantLookups := 0
	srv := New(Config{
		Secret: testSecret, GrantTap: grantTap,
		FindGrant: func(context.Context, string) (string, error) {
			grantLookups++
			return "", errors.New("must not look up a parked grant")
		},
		Authorize: allowAll, Now: func() time.Time { return fixedNow },
	})
	sink := withSink(srv)
	body := formBody(payloadJSON(grantrequest.ActionApprove, "gqr_abc", "michael"))
	req := signedRequest(testSecret, fixedNow, body)
	wantSignature := req.Header.Get(hdrSig)
	wantTimestamp := req.Header.Get(hdrTS)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ack status = %d, want 200", rec.Code)
	}
	sink.wait(t, 1)
	if !bytes.Equal(capturedBody, body) || capturedSignature != wantSignature || capturedTimestamp != wantTimestamp {
		t.Fatalf("Gate did not receive the original signed callback")
	}
	if grantLookups != 0 {
		t.Fatalf("grant request used parked-grant lookup %d times", grantLookups)
	}
	if got := sink.texts()[0]; !strings.Contains(got, "T0 approved") || !strings.Contains(got, "Gate can continue") {
		t.Fatalf("grant outcome card = %q", got)
	}
}

// TestServeHTTPBlockButtonMapsToBlock pins the other button: Block → gate's block
// decision, delivered as a blocked card. The ack is 200 (the tap was received);
// the block is a landed decision reported on the card, not a transport failure.
func TestServeHTTPBlockButtonMapsToBlock(t *testing.T) {
	cr := &captured{out: []byte(`{"outcome":"blocked"}`), code: 1}
	grantCalls := 0
	srv, sink := newServer(cr, fixedGrant("grt_live", &grantCalls))

	body := formBody(payloadJSON(escalation.ActionBlock, "esc_abc", "michael"))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, signedRequest(testSecret, fixedNow, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("ack status = %d, want 200", rec.Code)
	}
	sink.wait(t, 1)
	if len(cr.calls) != 1 || cr.calls[0][6] != "block" {
		t.Fatalf("want a single block resolve, got %v", cr.calls)
	}
	if got := sink.texts()[0]; !strings.Contains(got, "Blocked by @michael (U1)") {
		t.Fatalf("outcome card = %q, want a blocked card", got)
	}
}

// TestServeHTTPWhoFromVerifiedIdentity is the security-critical case: `who` is
// derived from the verified Slack identity, never from a client-settable field.
// The payload smuggles a top-level "who":"attacker"; the recorded who must be the
// signed user, and the smuggled field must be ignored by construction.
func TestServeHTTPWhoFromVerifiedIdentity(t *testing.T) {
	cr := &captured{out: []byte(`{}`), code: 0}
	grantCalls := 0
	srv, sink := newServer(cr, fixedGrant("grt_live", &grantCalls))

	hostile := `{"who":"attacker","type":"block_actions","user":{"id":"U9","username":"realuser"},` +
		`"response_url":"` + testResponseURL + `","actions":[{"action_id":"approve","value":"esc_beef"}]}`
	body := formBody(hostile)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, signedRequest(testSecret, fixedNow, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("ack status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	sink.wait(t, 1)
	argv := cr.calls[0]
	who := argv[len(argv)-3]
	if who != "@realuser (U9)" {
		t.Fatalf("who = %q, want @realuser (U9) — from the verified identity, not the payload's who field", who)
	}
	if strings.Contains(strings.Join(argv, " "), "attacker") {
		t.Fatalf("a client-supplied who must never reach gate: %v", argv)
	}
}

// TestServeHTTPAcksBeforeResolve is the core of the increment: the handler acks
// within Slack's ~3s window WITHOUT waiting for `gate resolve`. With the resolve
// blocked, ServeHTTP still returns 200, and the resolve is proven to still be
// in flight — the sync path never waits on the slow authoritative write.
func TestServeHTTPAcksBeforeResolve(t *testing.T) {
	release := make(chan struct{})
	resolved := make(chan struct{})
	runner := func(_ context.Context, _ string, _ ...string) ([]byte, int, error) {
		<-release
		close(resolved)
		return []byte(`{}`), 0, nil
	}
	srv := New(Config{
		Secret:    testSecret,
		Ingest:    ingest.New("gate", "", runner),
		FindGrant: func(_ context.Context, _ string) (string, error) { return "grt_live", nil },
		Authorize: allowAll,
		Now:       func() time.Time { return fixedNow },
	})
	sink := withSink(srv)

	body := formBody(payloadJSON(escalation.ActionApprove, "esc_abc", "michael"))
	rec := httptest.NewRecorder()
	acked := make(chan struct{})
	go func() {
		srv.ServeHTTP(rec, signedRequest(testSecret, fixedNow, body))
		close(acked)
	}()

	select {
	case <-acked:
	case <-time.After(2 * time.Second):
		t.Fatal("ServeHTTP did not ack while gate resolve was blocked — the handler is still synchronous")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("ack status = %d, want 200", rec.Code)
	}
	select {
	case <-resolved:
		t.Fatal("gate resolve completed before it was released — it did not run in the background")
	default:
	}
	close(release)
	sink.wait(t, 1)
}

// TestWaitDrainsInflight proves the graceful-drain seam: after a tap is acked,
// Wait blocks until that accepted resolve's background work finishes — so a
// shutdown can drain in-flight resolutions instead of dropping an acked-but-
// unrecorded decision (Slack won't retry a 200).
func TestWaitDrainsInflight(t *testing.T) {
	release := make(chan struct{})
	runner := func(_ context.Context, _ string, _ ...string) ([]byte, int, error) {
		<-release
		return []byte(`{}`), 0, nil
	}
	srv := New(Config{
		Secret:    testSecret,
		Ingest:    ingest.New("gate", "", runner),
		FindGrant: func(_ context.Context, _ string) (string, error) { return "grt_live", nil },
		Authorize: allowAll,
		Now:       func() time.Time { return fixedNow },
	})
	sink := withSink(srv)

	body := formBody(payloadJSON(escalation.ActionApprove, "esc_abc", "michael"))
	srv.ServeHTTP(httptest.NewRecorder(), signedRequest(testSecret, fixedNow, body))

	waited := make(chan struct{})
	go func() { srv.Wait(); close(waited) }()

	select {
	case <-waited:
		t.Fatal("Wait returned while a resolve was still in flight — an acked decision could be dropped on shutdown")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after the in-flight resolve drained")
	}
	sink.wait(t, 1)
}

// TestServeHTTPReplayForwardsBothToGuard proves serve does NOT implement its own
// idempotency: it forwards every signed tap to `gate resolve` and faithfully
// surfaces the guard's refusal on the second (as a gate-error card, gate exit 4).
// Slack retries and double-taps are made safe by gate's escalationIsOpen guard,
// not by serve swallowing the retry. The taps are sequenced (wait for the first
// outcome before the replay) so the ordering is deterministic.
func TestServeHTTPReplayForwardsBothToGuard(t *testing.T) {
	call := 0
	runner := func(_ context.Context, _ string, _ ...string) ([]byte, int, error) {
		call++
		if call == 1 {
			return []byte(`{"outcome":"would_merge"}`), 0, nil
		}
		return []byte(`{"outcome":"error","why":"escalation is not the run's open park"}`), 4, nil
	}
	srv := New(Config{
		Secret:    testSecret,
		Ingest:    ingest.New("gate", "", runner),
		FindGrant: func(_ context.Context, _ string) (string, error) { return "grt_live", nil },
		Authorize: allowAll,
		Now:       func() time.Time { return fixedNow },
	})
	sink := withSink(srv)

	body := formBody(payloadJSON(escalation.ActionApprove, "esc_deadbeef", "michael"))
	first := httptest.NewRecorder()
	srv.ServeHTTP(first, signedRequest(testSecret, fixedNow, body))
	sink.wait(t, 1) // the first tap resolves before the replay fires
	second := httptest.NewRecorder()
	srv.ServeHTTP(second, signedRequest(testSecret, fixedNow, body))
	sink.wait(t, 1)

	if call != 2 {
		t.Fatalf("serve must forward both taps to the guard, got %d resolve calls", call)
	}
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("both taps ack 200, got %d and %d", first.Code, second.Code)
	}
	texts := sink.texts()
	if !strings.Contains(texts[0], "Approved") {
		t.Fatalf("first card = %q, want the merge outcome", texts[0])
	}
	if !strings.Contains(texts[1], "Gate error") {
		t.Fatalf("second card = %q, want the guard's gate-error outcome", texts[1])
	}
}

// TestServeHTTPRejectsUnsigned proves rejection happens BEFORE the ack: a bad
// signature returns 401 and neither the grant lookup nor the resolve ever runs,
// so a forged callback cannot reach gate or spawn a background resolve.
func TestServeHTTPRejectsUnsigned(t *testing.T) {
	cr := &captured{out: []byte(`{}`), code: 0}
	grantCalls := 0
	srv, _ := newServer(cr, fixedGrant("grt_live", &grantCalls))

	body := formBody(payloadJSON(escalation.ActionApprove, "esc_abc", "michael"))
	req := signedRequest(testSecret, fixedNow, body)
	req.Header.Set(hdrSig, "v0=forged")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if grantCalls != 0 {
		t.Fatalf("grant finder must not run on an unsigned request (ran %d times)", grantCalls)
	}
	if len(cr.calls) != 0 {
		t.Fatalf("resolve must not run on an unsigned request (ran %d times)", len(cr.calls))
	}
}

// TestServeHTTPRejectsBadPayload covers the malformed-but-signed cases: an
// unknown action_id, a missing payload field, and an empty identity are each a
// synchronous 4xx that never acks and never reaches gate.
func TestServeHTTPRejectsBadPayload(t *testing.T) {
	cases := []struct {
		name string
		body []byte
	}{
		{"unknown action", formBody(payloadJSON("frobnicate", "esc_x", "michael"))},
		{"no payload field", []byte("notpayload=1")},
		{"empty identity", formBody(`{"type":"block_actions","user":{},"actions":[{"action_id":"approve","value":"esc_x"}]}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cr := &captured{code: 0}
			grantCalls := 0
			srv, _ := newServer(cr, fixedGrant("grt_live", &grantCalls))
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, signedRequest(testSecret, fixedNow, tc.body))
			if rec.Code < 400 || rec.Code >= 500 {
				t.Fatalf("status = %d, want 4xx", rec.Code)
			}
			if len(cr.calls) != 0 {
				t.Fatalf("resolve must not run on a bad payload")
			}
		})
	}
}

// TestServeHTTPNotParkedReportsAlreadyResolved pins the first line of the replay
// defense under the async model: a signed tap whose escalation is no longer
// parked acks 200 (the tap was received), never reaches gate resolve, and the
// delivered card reads as a benign "already resolved" — the path a real
// double-tap hits, since a resolved park drops out of the inbox serve reads.
func TestServeHTTPNotParkedReportsAlreadyResolved(t *testing.T) {
	cr := &captured{code: 0}
	notParked := func(_ context.Context, escID string) (string, error) {
		return "", fmt.Errorf("%w: %s not in gate inbox", ErrNotParked, escID)
	}
	srv, sink := newServer(cr, notParked)

	body := formBody(payloadJSON(escalation.ActionApprove, "esc_abc", "michael"))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, signedRequest(testSecret, fixedNow, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("ack status = %d, want 200", rec.Code)
	}
	sink.wait(t, 1)
	if len(cr.calls) != 0 {
		t.Fatalf("resolve must not run when the escalation is not parked")
	}
	if got := sink.texts()[0]; !strings.Contains(got, "Already resolved") {
		t.Fatalf("outcome card = %q, want an already-resolved card", got)
	}
}

// TestServeHTTPNoResponseURLStillResolves proves the decision is authoritative
// even when the callback carries no response_url: the resolve still runs (the
// decision lands in gate), only the card delivery is skipped. Delivery is
// best-effort presentation; the resolution is not.
func TestServeHTTPNoResponseURLStillResolves(t *testing.T) {
	var calls int32
	resolved := make(chan struct{})
	runner := func(_ context.Context, _ string, _ ...string) ([]byte, int, error) {
		atomic.AddInt32(&calls, 1)
		close(resolved)
		return []byte(`{}`), 0, nil
	}
	srv := New(Config{
		Secret:    testSecret,
		Ingest:    ingest.New("gate", "", runner),
		FindGrant: func(_ context.Context, _ string) (string, error) { return "grt_live", nil },
		Authorize: allowAll,
		Now:       func() time.Time { return fixedNow },
	})
	sink := withSink(srv)

	body := formBody(payloadJSONNoURL(escalation.ActionApprove, "esc_abc", "michael"))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, signedRequest(testSecret, fixedNow, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("ack status = %d, want 200", rec.Code)
	}
	select {
	case <-resolved:
	case <-time.After(2 * time.Second):
		t.Fatal("resolve did not run for a callback with no response_url")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("resolve ran %d times, want 1", got)
	}
	// No response_url means no card delivery — the sink must stay silent.
	select {
	case <-sink.done:
		t.Fatal("delivered a card despite the callback carrying no response_url")
	case <-time.After(100 * time.Millisecond):
	}
}

// TestServeHTTPSerializesSameEscalation proves the per-escalation lock: many
// concurrent taps for one escalation never have their resolves overlap, so two
// callbacks can't both pass gate's open-check before either records a terminal.
// Run under -race, it also asserts the lock itself is race-free.
func TestServeHTTPSerializesSameEscalation(t *testing.T) {
	var inflight, maxSeen int32
	runner := func(_ context.Context, _ string, _ ...string) ([]byte, int, error) {
		n := atomic.AddInt32(&inflight, 1)
		for {
			m := atomic.LoadInt32(&maxSeen)
			if n <= m || atomic.CompareAndSwapInt32(&maxSeen, m, n) {
				break
			}
		}
		time.Sleep(3 * time.Millisecond)
		atomic.AddInt32(&inflight, -1)
		return []byte(`{}`), 0, nil
	}
	srv := New(Config{
		Secret:    testSecret,
		Ingest:    ingest.New("gate", "", runner),
		FindGrant: func(_ context.Context, _ string) (string, error) { return "grt_live", nil },
		Authorize: allowAll,
		Now:       func() time.Time { return fixedNow },
	})
	sink := withSink(srv)

	body := formBody(payloadJSON(escalation.ActionApprove, "esc_abc", "michael"))
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			srv.ServeHTTP(httptest.NewRecorder(), signedRequest(testSecret, fixedNow, body))
		}()
	}
	wg.Wait()
	sink.wait(t, 8) // every tap's background resolve completes
	if maxSeen > 1 {
		t.Fatalf("same-escalation resolves overlapped (max inflight %d), want serialized to 1", maxSeen)
	}
}

// TestServeHTTPRejectsNonPost pins that only POST is accepted.
func TestServeHTTPRejectsNonPost(t *testing.T) {
	cr := &captured{code: 0}
	grantCalls := 0
	srv, _ := newServer(cr, fixedGrant("grt_live", &grantCalls))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// TestServeHTTPAuthorizesVerifiedUser is the authorization gate after
// authentication: with an allowlist of just U1, an authentic callback from U1
// acks and resolves, but an equally-authentic callback from an unlisted user is
// refused 403 before the ack and before any grant lookup or resolve — so a button
// visible to a whole channel still only resolves for the operator. The signature
// is valid in BOTH cases; what differs is who tapped.
func TestServeHTTPAuthorizesVerifiedUser(t *testing.T) {
	cr := &captured{out: []byte(`{}`), code: 0}
	grantCalls := 0
	srv := New(Config{
		Secret:    testSecret,
		Ingest:    ingest.New("gate", "", cr.runner),
		FindGrant: fixedGrant("grt_live", &grantCalls),
		Authorize: AllowUsers("U1"),
		Now:       func() time.Time { return fixedNow },
	})
	sink := withSink(srv)

	// payloadJSON stamps user id U1, who is on the allowlist → resolves.
	okBody := formBody(payloadJSON(escalation.ActionApprove, "esc_abc", "michael"))
	okRec := httptest.NewRecorder()
	srv.ServeHTTP(okRec, signedRequest(testSecret, fixedNow, okBody))
	if okRec.Code != http.StatusOK {
		t.Fatalf("allowlisted user status = %d, want 200; body %s", okRec.Code, okRec.Body)
	}
	sink.wait(t, 1)

	// A different, equally-authentic user (U2) is refused; gate is never driven.
	intruder := `{"type":"block_actions","user":{"id":"U2","username":"intruder"},` +
		`"response_url":"` + testResponseURL + `","actions":[{"action_id":"approve","value":"esc_no"}]}`
	noRec := httptest.NewRecorder()
	srv.ServeHTTP(noRec, signedRequest(testSecret, fixedNow, formBody(intruder)))
	if noRec.Code != http.StatusForbidden {
		t.Fatalf("unlisted user status = %d, want 403", noRec.Code)
	}
	if len(cr.calls) != 1 {
		t.Fatalf("only the allowlisted resolve should reach gate, got %d: %v", len(cr.calls), cr.calls)
	}
	if grantCalls != 1 {
		t.Fatalf("grant lookup ran %d times, want 1 (the refused tap never looks up)", grantCalls)
	}
}

func TestServeHTTPLogsResolvedWithoutUserIdentity(t *testing.T) {
	var calls int
	cr := &captured{out: []byte(`{"outcome":"would_merge"}`), code: codeMerge}
	s, _ := newServer(cr, fixedGrant("grt_log", &calls))
	var logs bytes.Buffer
	s.log = log.New(&logs, "", 0)

	body := formBody(payloadJSON(escalation.ActionApprove, "esc_0123456789abcdef", "private-user"))
	req := signedRequest(testSecret, fixedNow, body)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	s.Wait()

	got := logs.String()
	if !strings.Contains(got, "resolved escalation=esc_0123456789abcdef gate_exit=0 slack_updated=true") {
		t.Fatalf("successful resolution log missing:\n%s", got)
	}
	if strings.Contains(got, "private-user") {
		t.Fatalf("success logs must not expose Slack identity:\n%s", got)
	}
}

type blockingLogWriter struct {
	entered chan struct{}
	release chan struct{}
}

func (w *blockingLogWriter) Write(p []byte) (int, error) {
	close(w.entered)
	<-w.release
	return len(p), nil
}

func TestServeHTTPAcknowledgesBeforeLogging(t *testing.T) {
	var calls int
	cr := &captured{out: []byte(`{"outcome":"would_merge"}`), code: codeMerge}
	s, _ := newServer(cr, fixedGrant("grt_log", &calls))
	writer := &blockingLogWriter{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	s.log = log.New(writer, "", 0)

	body := formBody(payloadJSON(escalation.ActionApprove, "esc_0123456789abcdef", "private-user"))
	rec := httptest.NewRecorder()
	returned := make(chan struct{})
	go func() {
		s.ServeHTTP(rec, signedRequest(testSecret, fixedNow, body))
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("ack path blocked on logging")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("ack status = %d, want 200", rec.Code)
	}

	select {
	case <-writer.entered:
	case <-time.After(time.Second):
		t.Fatal("terminal resolution log was not attempted")
	}
	close(writer.release)
	s.Wait()
}

func TestServeHTTPLogsGateHardErrorAsFailed(t *testing.T) {
	var calls int
	cr := &captured{out: []byte(`{"outcome":"error"}`), code: 4}
	s, _ := newServer(cr, fixedGrant("grt_log", &calls))
	var logs bytes.Buffer
	s.log = log.New(&logs, "", 0)

	body := formBody(payloadJSON(escalation.ActionApprove, "esc_0123456789abcdef", "private-user"))
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, signedRequest(testSecret, fixedNow, body))
	s.Wait()

	got := logs.String()
	if !strings.Contains(got, "failed escalation=esc_0123456789abcdef gate_exit=4 slack_updated=true") {
		t.Fatalf("gate hard-error log missing:\n%s", got)
	}
	if strings.Contains(got, "resolved escalation=") {
		t.Fatalf("gate hard error must not be labeled resolved:\n%s", got)
	}
}

// TestAllowUsers pins the allowlist predicate: listed ids pass, unlisted and the
// empty id fail, and an EMPTY allowlist admits no one (fail-closed).
func TestAllowUsers(t *testing.T) {
	allow := AllowUsers("U1", "U2", "")
	for _, id := range []string{"U1", "U2"} {
		if !allow(id) {
			t.Fatalf("AllowUsers should admit listed id %q", id)
		}
	}
	for _, id := range []string{"U3", "", "u1"} {
		if allow(id) {
			t.Fatalf("AllowUsers must reject id %q", id)
		}
	}
	if AllowUsers()("U1") {
		t.Fatal("an empty allowlist must admit no one (fail-closed)")
	}
}

// TestOutcomeText pins the card vocabulary: each gate exit code maps to its
// outcome line, ErrNotParked reads as a benign already-resolved (not a failure),
// any other error is a generic "could not record", and an out-of-space code is a
// gate error the operator should retry.
func TestOutcomeText(t *testing.T) {
	cb := callback{decision: ingest.Decision{Who: "@michael (U1)"}}
	cases := []struct {
		name string
		code int
		err  error
		want string
	}{
		{"merge", 0, nil, "Approved by @michael (U1)"},
		{"blocked", 1, nil, "Blocked by @michael (U1)"},
		{"parked", 2, nil, "Re-parked"},
		{"refused", 3, nil, "Refused"},
		{"gate error", 4, nil, "Gate error"},
		{"not parked", 0, fmt.Errorf("%w: esc_x", ErrNotParked), "Already resolved"},
		// A grantless park is still parked — it must render out-of-band, never
		// the misleading "already resolved" its wrapped ErrNotParked would give.
		{"grantless park", 0, fmt.Errorf("%w: esc_g has no grant", ErrGrantlessPark), "out-of-band"},
		{"other error", 0, errors.New("boom"), "Could not record"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := outcomeText(cb, tc.code, tc.err); !strings.Contains(got, tc.want) {
				t.Fatalf("outcomeText = %q, want containing %q", got, tc.want)
			}
		})
	}
}

// TestCheckSlackURL is the SSRF guard: postResponse may POST only to an https URL
// on a slack.com host, so a forged-but-somehow-signed payload can never aim the
// ingress at an internal address.
func TestCheckSlackURL(t *testing.T) {
	for _, u := range []string{
		"https://hooks.slack.com/actions/T/B/x",
		"https://slack.com/x",
	} {
		if err := checkSlackURL(u); err != nil {
			t.Fatalf("checkSlackURL(%q) = %v, want nil", u, err)
		}
	}
	for _, u := range []string{
		"http://hooks.slack.com/x",           // not https
		"https://evil.com/x",                 // wrong host
		"https://hooks.slack.com.evil.com/x", // suffix trickery
		"https://127.0.0.1/x",                // internal
		"::not a url",                        // unparseable
	} {
		if err := checkSlackURL(u); err == nil {
			t.Fatalf("checkSlackURL(%q) = nil, want error", u)
		}
	}
}

// TestPostResponseRejectsNonSlackURL proves the guard runs inside the default
// transport: a non-Slack response_url is refused before any network call.
func TestPostResponseRejectsNonSlackURL(t *testing.T) {
	if err := postResponse(context.Background(), "https://evil.example/x", []byte(`{}`)); err == nil {
		t.Fatal("postResponse must refuse a non-Slack response_url")
	}
}
