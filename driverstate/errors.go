package driverstate

import (
	"errors"
	"fmt"
)

// The write-path errors are values with stable codes (spec §6). Callers branch
// on them with errors.As (the struct-carrying ones) or errors.Is (the
// sentinels); no path panics, and no write path silently skips.

// ErrLocked reports that a live lease is held by another writer. Holder names
// the actor recorded on the live lease so a second claimer fails fast with a
// message that says who to wait for (spec §7 F4).
type ErrLocked struct {
	Holder string
}

func (e ErrLocked) Error() string {
	if e.Holder == "" {
		return "driverstate: run is leased by a live writer"
	}
	return fmt.Sprintf("driverstate: run is leased by a live writer: %s", e.Holder)
}

// ErrIllegalTransition reports that recording Event on a stream (or run) at
// state From is not a legal move in the state machine (spec §5 table, §7 F2).
// From is the current derived status; Event is the rejected event kind.
type ErrIllegalTransition struct {
	From  string
	Event string
}

func (e ErrIllegalTransition) Error() string {
	return fmt.Sprintf("driverstate: illegal transition: cannot record %q from %q", e.Event, e.From)
}

// ErrChainBroken reports that a ledger's hash chain does not verify: a line
// failed to decode, or a stored hash does not seal its own bytes. The torn
// FINAL line is healed silently (truncated) on append; a break mid-chain is
// this loud error, never a silent truncation (spec §8).
var ErrChainBroken = errors.New("driverstate: hash chain broken")

// ErrNotHolder and ErrLeaseExpired are the DEFINITIVE lease-loss values: a lease
// call fails with one of these exactly when this holder no longer owns the run.
// ErrNotHolder — the record is gone, was self-released, or was stolen (a
// different generation/actor now holds it, also reported structurally as
// ErrLocked). ErrLeaseExpired — this holder's own lease lapsed. A caching caller
// (the MCP session lease map) evicts on these; a transient I/O error is NOT one
// of them and keeps the lease for a retry — use OwnershipLost to tell them apart.
var (
	ErrNotHolder    = errors.New("driverstate: caller does not hold the run lease; Claim first")
	ErrLeaseExpired = errors.New("driverstate: lease has expired; Renew or re-Claim")
	// errRetry is an internal transient marker: a lost O_EXCL race the retry
	// loop should re-attempt (never surfaced to callers).
	errRetry = errors.New("driverstate: transient contention")
	// errLockContended is what withRetry returns when the bounded retry budget
	// is exhausted on a lock still held — the caller-visible replacement for the
	// internal errRetry marker (a live writer holds the lock; try again later).
	errLockContended = errors.New("driverstate: lock still held after bounded retries")
)

// OwnershipLost reports whether err from Renew (or Append's write guard) means
// this holder has DEFINITIVELY lost the run lease — expired, stolen, or gone —
// and must re-Claim, as opposed to a transient failure (contention, a disk
// hiccup) that should simply be retried. It is the stable predicate a caching
// caller uses to decide eviction without branching on individual sentinels.
func OwnershipLost(err error) bool {
	return errors.Is(err, ErrLeaseExpired) ||
		errors.Is(err, ErrNotHolder) ||
		errors.As(err, new(ErrLocked))
}
