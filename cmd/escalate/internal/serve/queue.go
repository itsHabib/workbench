package serve

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"
)

// The burst contract: gate's state lock is a SINGLE-WRITER resource across every
// process on the box, and `gate resolve` waits only 10s for it before giving up.
// A tap that loses that wait records nothing — gate says so itself ("no judgment
// is recorded … the one judgment is unspent and a retry is legal") — so the
// correct response is to wait our turn and try again, not to report a failure.
//
// Measured (2026-09-05, ~/.flare/logs/escalate-serve.err.log): six taps inside
// ~10s, one resolved and five died on `state_lock_timeout` because serve ran
// every resolve concurrently and they contended with each other. Two mechanisms
// close that, both here: resolveQueue serializes this process's resolves so taps
// never contend with each OTHER, and the retry schedule below rides out a lock
// held by a DIFFERENT process (a long `gate gate` run holds it for minutes).
const (
	// resolveAttempts is the total number of `gate resolve` attempts one tap
	// gets — the first plus three retries. Each attempt absorbs gate's own 10s
	// lock wait, so the schedule spans ~90s of real contention before a tap is
	// reported failed.
	resolveAttempts = 4

	// resolveBudget bounds a tap's whole background life, measured from the ack:
	// queue wait plus backoff. It never cuts an attempt short — an attempt runs
	// under its own resolveTimeout — it only stops new ones from starting, so a
	// budget expiry can never kill `gate resolve` mid-append. Because every
	// budget starts at its own ack, a graceful drain is bounded by roughly one
	// window plus a final attempt rather than by the number of queued taps.
	resolveBudget = 3 * time.Minute

	// queueNotice is how long a tap may wait for its turn before the operator is
	// told it is queued. Under the normal burst (each resolve takes well under a
	// second) the queue drains inside this window and the card never flickers;
	// a wait long enough to notice gets a card that says so.
	queueNotice = 2 * time.Second

	// gateLockTimeout is the coded error gate prints when its state lock stayed
	// held — `state.ErrLockTimeout`, read off gate's OUTPUT rather than imported,
	// which is the boundary law (serve reads gate's artifacts, never its code).
	gateLockTimeout = "state_lock_timeout"
)

// resolveBackoff is the wait before retry 1, 2, and 3. It is a Server field
// (defaulted from here) so a test can compress it without touching a global.
var resolveBackoff = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}

// ErrStateBusy marks a gate invocation that failed ONLY because gate's state lock
// was held — by another tap, a CLI resolve, or a long `gate gate` run. It is the
// one failure this back-channel may retry: the lock is taken BEFORE any append,
// so a tap that lost it spent nothing and gate's own message says a retry is
// legal. Every other failure is reported as it happened, never retried.
var ErrStateBusy = errors.New("serve: gate state is busy")

// stateBusy reports whether a gate invocation's output names gate's lock
// timeout. gate prints its terminal error as JSON on stdout and as a line on
// stderr, so either stream answers the question; the caller passes what it has.
func stateBusy(out []byte) bool { return bytes.Contains(out, []byte(gateLockTimeout)) }

// decided reports whether a gate exit code is a landed decision (0..3). A
// decision is never retried, whatever else the output says — only a code outside
// that space can be a lock timeout.
func decided(code int) bool { return code >= codeMerge && code <= codeRefused }

// busy names a gate invocation that failed on the state lock, and nil for every
// other result. It is the one policy that decides what may be retried, so it is
// deliberately narrow: a landed decision is never busy however its output reads,
// and a failure whose output does not name gate's lock timeout is reported as it
// happened. gate prints the coded error on the stdout the caller already holds.
func busy(out []byte, code int) error {
	if decided(code) || !stateBusy(out) {
		return nil
	}
	return fmt.Errorf("%w: gate exited %d without taking its state lock, so nothing was recorded", ErrStateBusy, code)
}

// resolveQueue serializes every resolve this PROCESS runs, so concurrent taps
// take gate's state lock one at a time instead of racing each other for it. It
// is one slot, taken in FIFO order (Go wakes blocked channel senders in arrival
// order), held across a tap's whole lookup→resolve including its retries — a
// second waiter would only lose the same lock, and waiting is what the queue is
// for.
//
// It subsumes the per-escalation lock it replaced: same-escalation taps are
// still serialized (so gate's open-check and its terminal append can't interleave
// and double-apply a park), and now different escalations are too.
//
// SCOPE: this is one serve process behind one tunnel. It does not serialize a
// second serve process on the same -state, nor a CLI `escalate resolve` or a
// `gate gate` run racing a callback — those are exactly what the retry schedule
// rides out, and their durable fix is an atomic compare-and-resolve in gate,
// tracked in FOLLOWUPS.md.
type resolveQueue struct {
	slot chan struct{}
}

func newResolveQueue() *resolveQueue {
	return &resolveQueue{slot: make(chan struct{}, 1)}
}

// enter takes the queue's single slot and returns its release. A wait longer
// than notice calls announce exactly once (the timer fires once), so the caller
// can tell the operator their tap is queued without announcing a wait too short
// to matter. A cancelled ctx gives up the wait and returns its error — the tap
// then reports honestly that it never ran, rather than resolving late.
//
// announce runs ON this goroutine, which steps out of line for however long it
// takes. That is deliberate: announcing from a goroutine of its own would race
// the tap's own outcome card and could leave a resolved park showing "queued",
// and losing a place in a queue only costs latency.
func (q *resolveQueue) enter(ctx context.Context, notice time.Duration, announce func()) (func(), error) {
	timer := time.NewTimer(notice)
	defer timer.Stop()
	for {
		select {
		case q.slot <- struct{}{}:
			return func() { <-q.slot }, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			announce()
		}
	}
}

// wait sleeps for d unless ctx ends first, returning ctx's error if it does. The
// backoff between attempts is the only place a tap's budget can stop it, which is
// deliberate: an attempt in flight is never interrupted.
func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
