package e2e

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/workbench/contracts/escalation"
)

// operator is the verified Slack identity a tap carries: an immutable id and a
// mutable handle. serve records `who` as "@handle (id)" from THIS, never a
// client-settable field.
var operator = slackUser{ID: "U1MICHAEL", Username: "michael"}

// wantWho is what serve derives for `operator`: handle + immutable id.
const wantWho = "@michael (U1MICHAEL)"

// TestApproveTapResolvesPark is the happy path: a validly-signed Approve tap on a
// parked escalation is ACKED immediately (200), and the background resolve then
// drives gate to a merge. It proves the whole ngrok-carried input path —
// signature, the action_id→pass map, the who-from-verified-identity, the grant
// joined from the parked escalation — through the real serve binary, now that the
// authoritative work runs off the request path.
func TestApproveTapResolvesPark(t *testing.T) {
	s := startServer(t)
	code, body := s.tap(escalation.ActionApprove, operator)
	if code != 200 {
		t.Fatalf("approve tap status = %d, want 200 (ack); body: %s", code, body)
	}
	if !strings.Contains(body, "Recording") {
		t.Fatalf("ack should replace the card with a working state, got: %s", body)
	}
	inv := onlyInvocation(t, s)
	assertField(t, inv, "escalation", s.esc)
	assertField(t, inv, "grant", s.grant)
	assertField(t, inv, "decision", escalation.DecisionPass)
	assertField(t, inv, "who", wantWho)
	if inv["why"] == "" {
		t.Fatalf("resolve must carry a why, got %+v", inv)
	}
}

// TestBlockTapRecordsBlock pins that the Block button drives a block decision:
// the tap is acked (200), and the background resolve records gate's block verdict.
func TestBlockTapRecordsBlock(t *testing.T) {
	s := startServer(t)
	code, body := s.tap(escalation.ActionBlock, operator)
	if code != 200 {
		t.Fatalf("block tap status = %d, want 200 (ack); body: %s", code, body)
	}
	assertField(t, onlyInvocation(t, s), "decision", escalation.DecisionBlock)
}

// TestForgedSignatureRejected: a callback signed with the WRONG secret passes the
// timestamp window but fails the HMAC — rejected 401 before the payload is
// parsed, and gate is never driven.
func TestForgedSignatureRejected(t *testing.T) {
	s := startServer(t)
	body := callbackBody(escalation.ActionApprove, s.esc, operator)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	code, _ := s.post(sign("the-wrong-secret", ts, body), ts, body)
	if code != 401 {
		t.Fatalf("forged signature status = %d, want 401", code)
	}
	assertNoResolve(t, s)
}

// TestStaleTimestampRejected: a validly-signed callback whose timestamp is far
// outside the ±5-min window is rejected 401 on the window, even though the HMAC
// is correct — a captured callback cannot be replayed hours later.
func TestStaleTimestampRejected(t *testing.T) {
	s := startServer(t)
	body := callbackBody(escalation.ActionApprove, s.esc, operator)
	ts := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	code, _ := s.post(sign(s.secret, ts, body), ts, body)
	if code != 401 {
		t.Fatalf("stale timestamp status = %d, want 401", code)
	}
	assertNoResolve(t, s)
}

// TestUnsignedRejected: a callback with no signature headers is rejected before
// anything is trusted.
func TestUnsignedRejected(t *testing.T) {
	s := startServer(t)
	body := callbackBody(escalation.ActionApprove, s.esc, operator)
	code, _ := s.post("", "", body)
	if code != 401 {
		t.Fatalf("unsigned status = %d, want 401", code)
	}
	assertNoResolve(t, s)
}

// TestReplayResolvesOnce: the SAME valid tap fired twice resolves once. Both taps
// are acked (200), but the first resolves the park — which drops it from the inbox
// serve reads the grant from — so the second's background lookup finds no park and
// records nothing. This is the first line of the replay defense, ahead of gate's
// own open-check. The taps are sequenced (wait for the first to land) so the
// outcome is deterministic.
func TestReplayResolvesOnce(t *testing.T) {
	s := startServer(t)
	if code, body := s.tap(escalation.ActionApprove, operator); code != 200 {
		t.Fatalf("first tap status = %d, want 200 (ack); body: %s", code, body)
	}
	s.waitInvocations(1) // the first tap's background resolve lands before the replay
	if code, body := s.tap(escalation.ActionApprove, operator); code != 200 {
		t.Fatalf("replay status = %d, want 200 (ack); body: %s", code, body)
	}
	s.settle()
	if inv := s.invocations(); len(inv) != 1 {
		t.Fatalf("replay must resolve exactly once, got %d: %+v", len(inv), inv)
	}
}

// TestConcurrentDoubleTapResolvesOnce: two Approves fired at once — the
// concurrency the HTTP transport introduces — resolve exactly once. Both are
// acked (200); serve's per-escalation lock serializes the background resolves, so
// the winner resolves and the loser finds the park already closed.
func TestConcurrentDoubleTapResolvesOnce(t *testing.T) {
	s := startServer(t)
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i], _ = s.tap(escalation.ActionApprove, operator)
		}(i)
	}
	wg.Wait()
	if codes[0] != 200 || codes[1] != 200 {
		t.Fatalf("both taps should ack 200, got %v", codes)
	}
	s.waitInvocations(1)
	s.settle()
	if inv := s.invocations(); len(inv) != 1 {
		t.Fatalf("a double-tap must resolve exactly once, got %d: %+v", len(inv), inv)
	}
}

// TestForgedWhoIgnored: a top-level "who":"attacker" smuggled into the callback
// JSON is ignored — serve derives `who` only from the verified Slack identity, so
// the resolution records the real tapper, not the injected field.
func TestForgedWhoIgnored(t *testing.T) {
	s := startServer(t)
	body := callbackBodyWith(escalation.ActionApprove, s.esc, operator, `"who":"attacker",`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	code, _ := s.post(sign(s.secret, ts, body), ts, body)
	if code != 200 {
		t.Fatalf("tap status = %d, want 200", code)
	}
	who := onlyInvocation(t, s)["who"]
	if who == "attacker" || who != wantWho {
		t.Fatalf("who = %q, want the verified identity %q (a smuggled who must be ignored)", who, wantWho)
	}
}

// TestUnknownActionRejected: an action_id outside the Approve/Block vocabulary is
// rejected (400) rather than defaulted, so a malformed or unexpected button never
// silently resolves a park.
func TestUnknownActionRejected(t *testing.T) {
	s := startServer(t)
	body := callbackBody("delete-everything", s.esc, operator)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	code, _ := s.post(sign(s.secret, ts, body), ts, body)
	if code != 400 {
		t.Fatalf("unknown action status = %d, want 400", code)
	}
	assertNoResolve(t, s)
}

// TestUnauthorizedUserForbidden: an authentic tap from a Slack user NOT on the
// allowlist is refused 403 and never drives gate. The signature is valid (same
// secret) — what fails is authorization: the button is visible to the whole
// channel, but only the allowlisted operator can resolve. This is the authz gate
// the real tunnel needs before a channel-wide button is safe.
func TestUnauthorizedUserForbidden(t *testing.T) {
	s := startServer(t)
	intruder := slackUser{ID: "U9INTRUDER", Username: "intruder"}
	code, _ := s.tap(escalation.ActionApprove, intruder)
	if code != 403 {
		t.Fatalf("unlisted user status = %d, want 403", code)
	}
	assertNoResolve(t, s)
}

func onlyInvocation(t *testing.T, s *server) map[string]string {
	t.Helper()
	inv := s.waitInvocations(1)
	if len(inv) != 1 {
		t.Fatalf("want exactly one resolve invocation, got %d: %+v", len(inv), inv)
	}
	return inv[0]
}

func assertNoResolve(t *testing.T, s *server) {
	t.Helper()
	if inv := s.invocations(); len(inv) != 0 {
		t.Fatalf("gate must not be driven, got %+v", inv)
	}
}

func assertField(t *testing.T, inv map[string]string, key, want string) {
	t.Helper()
	if inv[key] != want {
		t.Fatalf("resolve %s = %q, want %q", key, inv[key], want)
	}
}
