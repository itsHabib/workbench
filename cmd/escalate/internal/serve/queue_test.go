package serve

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/escalate/internal/ingest"
	"github.com/itsHabib/workbench/contracts/escalation"
)

// gateLockTimeoutJSON is what gate prints on stdout when its state lock stayed
// held: the terminal-error envelope carrying the coded error. Copied from the
// 2026-09-05 burst log rather than imagined, since this string is the seam serve
// classifies on.
const gateLockTimeoutJSON = `{"error":"resolve: state_lock_timeout after 10s: open /Users/mh/dev/gate/state/log.lock: file exists",` +
	`"escape":{"why":"","next":""},"retry_helps":false}`

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

// TestBusyClassification pins the retry policy itself, which is the risky part:
// only a gate failure that names its lock timeout may be retried, and a LANDED
// decision never is — retrying one would double-apply a resolution.
func TestBusyClassification(t *testing.T) {
	cases := []struct {
		name string
		out  string
		code int
		want bool
	}{
		{"lock timeout", gateLockTimeoutJSON, codeError, true},
		{"merge", `{"outcome":"would_merge"}`, codeMerge, false},
		{"blocked", `{"outcome":"blocked"}`, codeBlocked, false},
		{"refused", `{"outcome":"refused"}`, codeRefused, false},
		{"other hard error", `{"error":"resolve: escalation is not the run's open park"}`, codeError, false},
		// A decision that landed is never retried, whatever its output says.
		{"decision quoting the lock error", gateLockTimeoutJSON, codeMerge, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := busy([]byte(tc.out), tc.code)
			if got := err != nil; got != tc.want {
				t.Fatalf("busy(%q, %d) = %v, want retryable=%t", tc.out, tc.code, err, tc.want)
			}
		})
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
