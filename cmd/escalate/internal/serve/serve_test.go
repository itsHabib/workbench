package serve

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
	"github.com/itsHabib/workbench/contracts/escalation"
)

var testSecret = []byte("8f742231b10c8537228d4e5a1a1a2d3f")

// fixedNow anchors the timestamp window so signing and verification agree on
// "now" without touching the wall clock.
var fixedNow = time.Unix(1_700_000_000, 0)

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

// allowAll authorizes every user; the shared constructor uses it so tests that
// exercise the authn / mapping / replay paths are not entangled with the authz
// gate, which TestUnauthorizedUserForbidden covers on its own.
func allowAll(string) bool { return true }

func newServer(cr *captured, findGrant GrantFinder) *Server {
	return New(Config{
		Secret:    testSecret,
		Ingest:    ingest.New("gate", "", cr.runner),
		FindGrant: findGrant,
		Authorize: allowAll,
		Now:       func() time.Time { return fixedNow },
	})
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
// Approve callback drives `gate resolve` with the verdict, escalation id, grant,
// who, and why derived from the request — and returns gate's outcome on 200.
func TestServeHTTPMapsPayloadToDecision(t *testing.T) {
	cr := &captured{out: []byte(`{"outcome":"would_merge"}`), code: 0}
	grantCalls := 0
	srv := newServer(cr, fixedGrant("grt_live", &grantCalls))

	body := formBody(payloadJSON(escalation.ActionApprove, "esc_abc", "michael"))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, signedRequest(testSecret, fixedNow, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
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
	}
	if !reflect.DeepEqual(cr.calls[0], want) {
		t.Fatalf("argv mismatch:\n got=%v\nwant=%v", cr.calls[0], want)
	}
	if strings.TrimSpace(rec.Body.String()) != `{"outcome":"would_merge"}` {
		t.Fatalf("gate outcome must pass through, got %s", rec.Body)
	}
}

// TestServeHTTPBlockButtonMapsToBlock pins the other button: Block → gate's
// block decision, with a matching why. The status is 200 — the callback was
// ingested and drove gate; the block is a landed decision reported in the body,
// not a transport failure (so Slack never retries it).
func TestServeHTTPBlockButtonMapsToBlock(t *testing.T) {
	cr := &captured{out: []byte(`{"outcome":"blocked"}`), code: 1}
	grantCalls := 0
	srv := newServer(cr, fixedGrant("grt_live", &grantCalls))

	body := formBody(payloadJSON(escalation.ActionBlock, "esc_abc", "michael"))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, signedRequest(testSecret, fixedNow, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("a landed decision should be 200, got %d", rec.Code)
	}
	if len(cr.calls) != 1 || cr.calls[0][6] != "block" {
		t.Fatalf("want a single block resolve, got %v", cr.calls)
	}
}

// TestServeHTTPWhoFromVerifiedIdentity is the security-critical case: `who` is
// derived from the verified Slack identity, never from a client-settable field.
// The payload smuggles a top-level "who":"attacker"; the recorded who must be
// the signed user, and the smuggled field must be ignored by construction.
func TestServeHTTPWhoFromVerifiedIdentity(t *testing.T) {
	cr := &captured{out: []byte(`{}`), code: 0}
	grantCalls := 0
	srv := newServer(cr, fixedGrant("grt_live", &grantCalls))

	hostile := `{"who":"attacker","type":"block_actions","user":{"id":"U9","username":"realuser"},` +
		`"actions":[{"action_id":"approve","value":"esc_beef"}]}`
	body := formBody(hostile)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, signedRequest(testSecret, fixedNow, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	argv := cr.calls[0]
	who := argv[len(argv)-1]
	if who != "@realuser (U9)" {
		t.Fatalf("who = %q, want @realuser (U9) — from the verified identity, not the payload's who field", who)
	}
	if strings.Contains(strings.Join(argv, " "), "attacker") {
		t.Fatalf("a client-supplied who must never reach gate: %v", argv)
	}
}

// TestServeHTTPRejectsUnsigned proves rejection happens BEFORE the decision
// path: a bad signature returns 401 and neither the grant lookup nor the resolve
// ever runs, so a forged callback cannot reach gate.
func TestServeHTTPRejectsUnsigned(t *testing.T) {
	cr := &captured{out: []byte(`{}`), code: 0}
	grantCalls := 0
	srv := newServer(cr, fixedGrant("grt_live", &grantCalls))

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

// TestServeHTTPReplayLeansOnGuard proves serve does NOT implement its own
// idempotency: it forwards every signed tap to `gate resolve` and faithfully
// surfaces the guard's refusal on the second. Slack retries and double-taps are
// made safe by gate's escalationIsOpen guard (already merged), not by serve
// swallowing the retry — so the replayed tap reaches gate and is refused there.
func TestServeHTTPReplayLeansOnGuard(t *testing.T) {
	call := 0
	// First tap merges; the replayed tap hits gate's guard, which refuses a park
	// that is no longer open (gate exit 4 with its own diagnostic).
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

	body := formBody(payloadJSON(escalation.ActionApprove, "esc_deadbeef", "michael"))
	first := httptest.NewRecorder()
	srv.ServeHTTP(first, signedRequest(testSecret, fixedNow, body))
	second := httptest.NewRecorder()
	srv.ServeHTTP(second, signedRequest(testSecret, fixedNow, body))

	if call != 2 {
		t.Fatalf("serve must forward both taps to the guard, got %d resolve calls", call)
	}
	if first.Code != http.StatusOK {
		t.Fatalf("first tap should merge (200), got %d", first.Code)
	}
	// The replayed tap reached gate and gate's guard refused it with exit 4. serve
	// did not dedupe — the guard did — and it surfaces the refusal as a 502 (no
	// new decision landed), with gate's "open park" diagnostic in the body.
	if second.Code != http.StatusBadGateway {
		t.Fatalf("a gate exit-4 refusal should be 502, got %d", second.Code)
	}
	if !strings.Contains(second.Body.String(), "open park") {
		t.Fatalf("the guard's refusal diagnostic must pass through, got %s", second.Body)
	}
}

// TestServeHTTPRejectsBadPayload covers the malformed-but-signed cases: an
// unknown action_id, a missing payload field, and an empty identity are each a
// 4xx that never reaches gate.
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
			srv := newServer(cr, fixedGrant("grt_live", &grantCalls))
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

// TestServeHTTPNotParkedIsConflict pins the first line of the replay defense: a
// signed tap whose escalation is no longer parked (already resolved, superseded,
// or never parked) is a clean 409 — an "already handled" — not a server error,
// and it never reaches gate. This is the path a real double-tap hits, since a
// resolved park drops out of the inbox serve reads the grant from.
func TestServeHTTPNotParkedIsConflict(t *testing.T) {
	cr := &captured{code: 0}
	notParked := func(_ context.Context, escID string) (string, error) {
		return "", fmt.Errorf("%w: %s not in gate inbox", ErrNotParked, escID)
	}
	srv := newServer(cr, notParked)

	body := formBody(payloadJSON(escalation.ActionApprove, "esc_abc", "michael"))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, signedRequest(testSecret, fixedNow, body))

	if rec.Code != http.StatusConflict {
		t.Fatalf("a not-parked escalation should be 409, got %d", rec.Code)
	}
	if len(cr.calls) != 0 {
		t.Fatalf("resolve must not run when the escalation is not parked")
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
	if maxSeen > 1 {
		t.Fatalf("same-escalation resolves overlapped (max inflight %d), want serialized to 1", maxSeen)
	}
}

// TestServeHTTPRejectsNonPost pins that only POST is accepted.
func TestServeHTTPRejectsNonPost(t *testing.T) {
	cr := &captured{code: 0}
	grantCalls := 0
	srv := newServer(cr, fixedGrant("grt_live", &grantCalls))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

// TestServeHTTPAuthorizesVerifiedUser is the authorization gate after
// authentication: with an allowlist of just U1, an authentic callback from U1
// resolves, but an equally-authentic callback from an unlisted user is refused
// 403 before any grant lookup or resolve — so a button visible to a whole
// channel still only resolves for the operator. The signature is valid in BOTH
// cases; what differs is who tapped.
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

	// payloadJSON stamps user id U1, who is on the allowlist → resolves.
	okBody := formBody(payloadJSON(escalation.ActionApprove, "esc_abc", "michael"))
	okRec := httptest.NewRecorder()
	srv.ServeHTTP(okRec, signedRequest(testSecret, fixedNow, okBody))
	if okRec.Code != http.StatusOK {
		t.Fatalf("allowlisted user status = %d, want 200; body %s", okRec.Code, okRec.Body)
	}

	// A different, equally-authentic user (U2) is refused; gate is never driven.
	intruder := `{"type":"block_actions","user":{"id":"U2","username":"intruder"},` +
		`"actions":[{"action_id":"approve","value":"esc_no"}]}`
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
