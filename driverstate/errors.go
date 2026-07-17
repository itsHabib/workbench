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

// errNoLease and errLeaseExpired are internal write-guard failures surfaced
// when Append is called without a live lease. They are not part of the public
// stable-code set; callers re-Claim rather than branch on them.
var (
	errNoLease      = errors.New("driverstate: no lease held for run; Claim first")
	errLeaseExpired = errors.New("driverstate: lease has expired; Renew or re-Claim")
	// errRetry is an internal transient marker: a lost O_EXCL race the retry
	// loop should re-attempt (never surfaced to callers).
	errRetry = errors.New("driverstate: transient contention")
)
