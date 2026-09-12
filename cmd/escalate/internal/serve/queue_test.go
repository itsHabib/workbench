package serve

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/escalate/internal/ingest"
	"github.com/itsHabib/workbench/contracts/escalation"
	"github.com/itsHabib/workbench/contracts/grantrequest"
)

// gateLockTimeoutJSON is what gate prints on stdout when a resolve loses its
// state lock BEFORE writing anything: the terminal-error envelope carrying the
// coded error and gate's own re-read of the run, which says the judgment is
// unspent. Copied from the 2026-09-05 burst log rather than imagined, since both
// halves of this string are the seam serve classifies on.
const gateLockTimeoutJSON = `{"error":"resolve: state_lock_timeout after 10s: open /Users/mh/dev/gate/state/log.lock: file exists; ` +
	`no judgment is recorded for esc_ab — the one judgment is unspent and a retry is legal",` +
	`"escape":{"why":"","next":""},"retry_helps":false}`

// gateLockReadTimeoutJSON is the OTHER pre-append shape: the lock lost in the
// reads that resolve the escalation to its run and check the park is still
// open, before gate appends anything. It carries preAppendFailure's annotation
// rather than judgeSlotState's, and serve keys on the phrase both share.
const gateLockReadTimeoutJSON = `{"error":"resolve: escalation esc_ab: state_lock_timeout after 10s: ` +
	`open /Users/mh/dev/gate/state/log.lock: file exists; this failure landed before any append — ` +
	`nothing was recorded and a retry is legal","escape":{"why":"","next":""},"retry_helps":false}`

// gateLockAfterAppendJSON is the same lock timeout landing LATER in the same
// resolve: gate appended the decision and then lost the lock stamping the
// resolution. The stamp's append returns the store's error bare, so no
// annotation is attached — which is the point: the decision is recorded, and
// retrying would find the park closed and report a benign "already resolved"
// over a missing stamp.
const gateLockAfterAppendJSON = `{"error":"state_lock_timeout after 10s: ` +
	`open /Users/mh/dev/gate/state/log.lock: file exists","escape":{"why":"","next":""},"retry_helps":false}`

// fakeGate models the one property of gate that produced the burst failure: its
// state log is guarded by a single lock, taken by ONE process at a time, and an
// invocation that cannot take it within its wait gives up with the coded error
// above — having written nothing. Real gate waits 10s; this waits milliseconds,
// so the whole contended schedule runs in a test.
type fakeGate struct {
	slot chan struct{} // gate's state lock: one holder, cross-process in reality
	wait time.Duration // what gate's 10s lock wait stands for here

	mu       sync.Mutex
	resolved []string // the escalation ids that actually reached the log
	calls    int
	inflight atomic.Int32
	maxSeen  atomic.Int32
}

func newFakeGate(wait time.Duration) *fakeGate {
	return &fakeGate{slot: make(chan struct{}, 1), wait: wait}
}

// hold takes gate's lock the way another process would — a long `gate gate`
// consolidation run, or a CLI resolve — and returns its release.
func (g *fakeGate) hold() func() {
	g.slot <- struct{}{}
	return func() { <-g.slot }
}

// runner is the ingest.Runner serve drives. It records concurrency (so a test can
// prove serve never runs two gate invocations at once), then either takes the
// lock and journals the resolve or times out exactly as gate does.
func (g *fakeGate) runner(ctx context.Context, _ string, args ...string) ([]byte, int, error) {
	n := g.inflight.Add(1)
	defer g.inflight.Add(-1)
	for {
		peak := g.maxSeen.Load()
		if n <= peak || g.maxSeen.CompareAndSwap(peak, n) {
			break
		}
	}
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()

	timer := time.NewTimer(g.wait)
	defer timer.Stop()
	select {
	case g.slot <- struct{}{}:
	case <-timer.C:
		return []byte(gateLockTimeoutJSON), codeError, nil
	case <-ctx.Done():
		return nil, -1, ctx.Err()
	}
	defer func() { <-g.slot }()

	g.mu.Lock()
	g.resolved = append(g.resolved, escArg(args))
	g.mu.Unlock()
	return []byte(`{"outcome":"would_merge"}`), codeMerge, nil
}

func (g *fakeGate) journal() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.resolved...)
}

func (g *fakeGate) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

// escArg reads the escalation id out of the argv serve shelled.
func escArg(args []string) string {
	for i, a := range args {
		if a == "-escalation" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// burstServer builds a Server over the fake gate with the retry schedule
// compressed to test time. The schedule's SHAPE (first try, then retries with a
// wait between) is what is under test; its production durations are not.
func burstServer(g *fakeGate, backoff time.Duration) (*Server, *deliverSink) {
	srv := New(Config{
		Secret:    testSecret,
		Ingest:    ingest.New("gate", "", g.runner),
		FindGrant: func(_ context.Context, _ string) (string, error) { return "grt_live", nil },
		Authorize: allowAll,
		Now:       func() time.Time { return fixedNow },
	})
	srv.backoff = []time.Duration{backoff, backoff, backoff}
	srv.notice = time.Millisecond
	return srv, withSink(srv)
}

// countCards polls the sink until n cards contain marker, so a test can wait for
// the FINAL outcomes while interim queued/retrying cards land on the same sink.
func countCards(t *testing.T, sink *deliverSink, marker string, n int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cards(sink, marker) >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out with %d of %d cards containing %q; saw %v", cards(sink, marker), n, marker, sink.texts())
}

func cards(sink *deliverSink, marker string) int {
	n := 0
	for _, text := range sink.texts() {
		if strings.Contains(text, marker) {
			n++
		}
	}
	return n
}

// TestBurstResolvesEveryTap is the regression the whole change exists for. On
// 2026-09-05 six taps landed inside ~10s: serve ran every resolve concurrently,
// they contended for gate's single state lock, and five of the six died on
// state_lock_timeout with nothing recorded and nothing retried. Here the same
// burst arrives while ANOTHER process holds the lock for longer than a gate
// invocation will wait for it — the worst case, contention from outside this
// process on top of contention within it — and every tap must still resolve
// exactly once, with no two gate invocations ever running at the same time.
func TestBurstResolvesEveryTap(t *testing.T) {
	const taps = 6
	gate := newFakeGate(20 * time.Millisecond)
	srv, sink := burstServer(gate, 40*time.Millisecond)

	// A long-running gate process holds the log lock across the burst, so the
	// first attempts cannot take it and must be retried rather than reported.
	release := gate.hold()
	go func() {
		time.Sleep(100 * time.Millisecond)
		release()
	}()

	var wg sync.WaitGroup
	for i := range taps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := formBody(payloadJSON(escalation.ActionApprove, fmt.Sprintf("esc_%02x", i), "michael"))
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, signedRequest(testSecret, fixedNow, body))
			if rec.Code != 200 {
				t.Errorf("tap %d ack = %d, want 200", i, rec.Code)
			}
		}()
	}
	wg.Wait()
	countCards(t, sink, "Approved by", taps)
	srv.Wait()

	journal := gate.journal()
	if len(journal) != taps {
		t.Fatalf("gate recorded %d resolutions, want %d — a tap was lost: %v", len(journal), taps, journal)
	}
	seen := make(map[string]int, taps)
	for _, esc := range journal {
		seen[esc]++
	}
	for i := range taps {
		esc := fmt.Sprintf("esc_%02x", i)
		if seen[esc] != 1 {
			t.Fatalf("escalation %s resolved %d times, want exactly 1: %v", esc, seen[esc], journal)
		}
	}
	if got := gate.maxSeen.Load(); got != 1 {
		t.Fatalf("%d gate invocations ran at once — taps must be serialized so they never contend for the state lock", got)
	}
}

// TestBurstCardStaysHonest pins the operator-facing half: while a tap waits its
// turn or rides out the lock, its card says so, and the failure vocabulary is
// reserved for a tap that actually ran out of retries. A card that says "failed"
// while the decision is still coming is exactly the lie the burst told.
func TestBurstCardStaysHonest(t *testing.T) {
	gate := newFakeGate(10 * time.Millisecond)
	srv, sink := burstServer(gate, 20*time.Millisecond)

	release := gate.hold()
	go func() {
		time.Sleep(80 * time.Millisecond)
		release()
	}()

	var wg sync.WaitGroup
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			body := formBody(payloadJSON(escalation.ActionApprove, fmt.Sprintf("esc_%02x", i), "michael"))
			srv.ServeHTTP(httptest.NewRecorder(), signedRequest(testSecret, fixedNow, body))
		}()
	}
	wg.Wait()
	countCards(t, sink, "Approved by", 2)
	srv.Wait()

	if got := cards(sink, "still trying"); got == 0 {
		t.Fatalf("no retrying card while gate was busy; saw %v", sink.texts())
	}
	if got := cards(sink, "Queued"); got == 0 {
		t.Fatalf("no queued card while a tap waited its turn; saw %v", sink.texts())
	}
	if got := cards(sink, "NOT recorded"); got != 0 {
		t.Fatalf("%d taps were reported failed although both resolved: %v", got, sink.texts())
	}
}

// TestBurstGivesUpAfterAttempts is the other end of the contract: retries are
// bounded. With the lock held for the whole test, one tap tries resolveAttempts
// times, records nothing, and gets a card that says the decision was NOT recorded
// — never a card claiming an outcome gate did not produce.
func TestBurstGivesUpAfterAttempts(t *testing.T) {
	gate := newFakeGate(5 * time.Millisecond)
	srv, sink := burstServer(gate, 5*time.Millisecond)
	defer gate.hold()() // held for the duration: every attempt times out

	body := formBody(payloadJSON(escalation.ActionApprove, "esc_ab", "michael"))
	srv.ServeHTTP(httptest.NewRecorder(), signedRequest(testSecret, fixedNow, body))
	countCards(t, sink, "NOT recorded", 1)
	srv.Wait()

	if got := gate.callCount(); got != resolveAttempts {
		t.Fatalf("gate was called %d times, want %d attempts", got, resolveAttempts)
	}
	if journal := gate.journal(); len(journal) != 0 {
		t.Fatalf("a timed-out resolve must record nothing, got %v", journal)
	}
}

// TestGrantCallbackIsNotQueuedBehindResolves is the loss path the queue must not
// create. A grant callback carries a signature gate re-verifies on arrival, so a
// queue deep enough to outlast it would consume the operator's tap and apply
// nothing — the tap is acked and its buttons are gone, so there is no second one.
// It is therefore forwarded while a park resolve holds the slot, exactly as it
// was before the queue existed.
func TestGrantCallbackIsNotQueuedBehindResolves(t *testing.T) {
	held := make(chan struct{})
	holding := make(chan struct{})
	forwarded := make(chan struct{})
	parkRunner := func(context.Context, string, ...string) ([]byte, int, error) {
		close(holding) // the slot is now taken, and stays taken until `held` closes
		<-held
		return []byte(`{"outcome":"would_merge"}`), codeMerge, nil
	}
	grantTap := func(context.Context, []byte, string, string) ([]byte, int, error) {
		close(forwarded)
		return []byte(`{"outcome":"granted","repo":"o/r","pr":7,"head_sha":"aaaaaaaa"}`), codeMerge, nil
	}
	srv := New(Config{
		Secret:    testSecret,
		Ingest:    ingest.New("gate", "", parkRunner),
		FindGrant: func(context.Context, string) (string, error) { return "grt_live", nil },
		GrantTap:  grantTap,
		Authorize: allowAll,
		Now:       func() time.Time { return fixedNow },
	})
	sink := withSink(srv)

	park := formBody(payloadJSON(escalation.ActionApprove, "esc_ab", "michael"))
	srv.ServeHTTP(httptest.NewRecorder(), signedRequest(testSecret, fixedNow, park))
	select {
	case <-holding:
	case <-time.After(2 * time.Second):
		close(held)
		t.Fatal("the park resolve never took the queue slot, so nothing is being tested")
	}
	grant := formBody(payloadJSON(grantrequest.ActionApprove, "gqr_abc", "michael"))
	srv.ServeHTTP(httptest.NewRecorder(), signedRequest(testSecret, fixedNow, grant))

	select {
	case <-forwarded:
	case <-time.After(2 * time.Second):
		close(held)
		t.Fatal("the grant callback waited on the queue — its signature drains while it waits")
	}
	close(held)
	countCards(t, sink, "T0 approved", 1)
	countCards(t, sink, "Approved by", 1)
	srv.Wait()
}

// TestBusyClassification pins the retry policy itself, which is the risky part.
// A resolve is retried only when gate reports its lock timeout AND says the
// judgment is unspent; a landed decision never is, and neither is a lock lost
// after gate already appended the decision — retrying either would report a
// benign outcome over a resolution that is missing its stamp or applied twice.
// A grant callback needs no such annotation: its whole effect is one single-use
// append gate excludes atomically.
func TestBusyClassification(t *testing.T) {
	cases := []struct {
		name        string
		out         string
		code        int
		wantResolve bool
		wantGrant   bool
	}{
		{"lock timeout before any append", gateLockTimeoutJSON, codeError, true, true},
		{"lock timeout in the pre-append reads", gateLockReadTimeoutJSON, codeError, true, true},
		{"lock timeout after the decision landed", gateLockAfterAppendJSON, codeError, false, true},
		{"merge", `{"outcome":"would_merge"}`, codeMerge, false, false},
		{"blocked", `{"outcome":"blocked"}`, codeBlocked, false, false},
		{"refused", `{"outcome":"refused"}`, codeRefused, false, false},
		{"other hard error", `{"error":"resolve: escalation is not the run's open park"}`, codeError, false, false},
		// A decision that landed is never retried, whatever its output says.
		{"decision quoting the lock error", gateLockTimeoutJSON, codeMerge, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveBusy([]byte(tc.out), tc.code) != nil; got != tc.wantResolve {
				t.Fatalf("resolveBusy(%q, %d) retryable=%t, want %t", tc.out, tc.code, got, tc.wantResolve)
			}
			if got := busy([]byte(tc.out), tc.code) != nil; got != tc.wantGrant {
				t.Fatalf("busy(%q, %d) retryable=%t, want %t", tc.out, tc.code, got, tc.wantGrant)
			}
		})
	}
}

// TestLockLostAfterTheDecisionLandedIsNotRetried is the end-to-end half of that
// policy, and the case a retry keyed on the lock timeout alone gets wrong: gate
// recorded the decision and then lost the lock stamping the resolution. serve
// must NOT retry — a retry finds the park closed and reads as "already resolved"
// — and must show the gate-error card that sends the operator to look.
func TestLockLostAfterTheDecisionLandedIsNotRetried(t *testing.T) {
	var calls atomic.Int32
	runner := func(context.Context, string, ...string) ([]byte, int, error) {
		calls.Add(1)
		return []byte(gateLockAfterAppendJSON), codeError, nil
	}
	srv := New(Config{
		Secret:    testSecret,
		Ingest:    ingest.New("gate", "", runner),
		FindGrant: func(context.Context, string) (string, error) { return "grt_live", nil },
		Authorize: allowAll,
		Now:       func() time.Time { return fixedNow },
	})
	sink := withSink(srv)
	srv.backoff = []time.Duration{time.Millisecond}

	body := formBody(payloadJSON(escalation.ActionApprove, "esc_ab", "michael"))
	srv.ServeHTTP(httptest.NewRecorder(), signedRequest(testSecret, fixedNow, body))
	sink.wait(t, 1)
	srv.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("gate was called %d times; a lock lost after the decision landed must not be retried", got)
	}
	if got := sink.texts()[0]; !strings.Contains(got, "Gate error") {
		t.Fatalf("card = %q, want the gate-error card that sends the operator to look", got)
	}
}

// TestGrantCallbackBudgetTracksItsSignature pins the other half of the queue's
// cost: gate re-verifies the same Slack signature a grant callback carries, so
// waiting past Slack's ±5-min window turns a decision into a refusal. A grant
// tap's budget is the life left on its signature, never the park budget, and a
// park tap is unaffected.
func TestGrantCallbackBudgetTracksItsSignature(t *testing.T) {
	srv := New(Config{Secret: testSecret, Authorize: allowAll, Now: func() time.Time { return fixedNow }})
	stamp := func(at time.Time) string { return strconv.FormatInt(at.Unix(), 10) }

	park := callback{timestamp: stamp(fixedNow.Add(-4 * time.Minute))}
	if got := srv.budgetFor(park); got != resolveBudget {
		t.Fatalf("park budget = %s, want the ordinary %s", got, resolveBudget)
	}
	fresh := callback{grantRequest: true, timestamp: stamp(fixedNow)}
	if got := srv.budgetFor(fresh); got != resolveBudget {
		t.Fatalf("fresh grant budget = %s, want it capped at %s", got, resolveBudget)
	}
	aging := callback{grantRequest: true, timestamp: stamp(fixedNow.Add(-4 * time.Minute))}
	if want := maxSkew - 4*time.Minute - signatureHeadroom; srv.budgetFor(aging) != want {
		t.Fatalf("aging grant budget = %s, want the %s left on its signature", srv.budgetFor(aging), want)
	}
	stale := callback{grantRequest: true, timestamp: stamp(fixedNow.Add(-6 * time.Minute))}
	if got := srv.budgetFor(stale); got != 0 {
		t.Fatalf("stale grant budget = %s, want none — it may still attempt, but never wait", got)
	}
	if got := srv.budgetFor(callback{grantRequest: true, timestamp: "nonsense"}); got != 0 {
		t.Fatalf("unparseable timestamp budget = %s, want none", got)
	}
}

// TestResolveQueueGivesUpWhenBudgetEnds proves a queued tap cannot wait forever:
// when its budget ends before its turn comes, enter returns the context's error
// so the tap reports that it never ran instead of resolving minutes late.
func TestResolveQueueGivesUpWhenBudgetEnds(t *testing.T) {
	q := newResolveQueue()
	release, err := q.enter(context.Background(), time.Hour, func() {})
	if err != nil {
		t.Fatalf("first enter = %v, want the free slot", err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	announced := 0
	if _, err := q.enter(ctx, time.Millisecond, func() { announced++ }); err == nil {
		t.Fatal("second enter took an occupied slot")
	}
	if announced != 1 {
		t.Fatalf("announce ran %d times, want exactly one queued notice", announced)
	}
}
