// Package driverstate is the write-side MECHANISM of the driver-state plane:
// durable single-writer-per-run leases and hash-chained, crash-safe appends.
//
// It is a shared-mechanism package in the mould of local/ — it carries the
// contract's write semantics, not any tool's policy, and it imports at most
// contracts (leaf-checked by CI's hygiene job). Readers of the ledger (Reduce,
// Runs, Verify) land in the sibling read-path change; this file's scope is
// Claim/Renew/Release and Append.
//
// # The lease
//
// A run has one durable lease file, lease.json, under its run directory. Claim
// takes it, Renew heartbeats it, Release drops it. Staleness is EXPIRY, not PID
// liveness: the lease records {actor, pid, expires_at, generation}, and a killed
// session's lease self-clears within one DefaultLeaseTTL window because a later
// Claim sees an expired record and steals it. The generation counter increases
// on every steal so a stale holder that wakes up detects it lost the lease. Exclusivity comes from O_EXCL create + an
// atomic generation-suffixed rename for the steal — the same lock discipline
// gate uses, including the Windows delete-pending → retry-everything lesson
// (every filesystem step runs under a bounded retry loop).
//
// The run lease is what enforces single-writer-per-run across a whole session.
// The per-append lock (append.lock) inside Append only prevents byte races; on
// its own it could not deliver ErrLocked, which is a lease promise.
//
// # The chain rule (kept dead simple so ship's TS emitter reproduces it)
//
// Each run's events live in events.jsonl, one canonical event per line. Append:
//
//  1. acquire the append lock,
//  2. truncate any torn tail to the last complete newline (a crash's partial
//     line must never corrupt the next event),
//  3. read the head INSIDE the lock — chain, time-monotonicity, and
//     stream_attempt seq validation all use this one read,
//  4. seal: Prev = the head event's Hash (empty for the first event), and
//     Hash = SHA-256 over contracts/driverstate.Canonical(event),
//  5. write the canonical bytes + '\n', fsync, release.
//
// The canonical encoding is contracts/driverstate's — the pinned P1 reference
// vector is the cross-language truth. Append writes contracts.EncodeEvent bytes
// verbatim (never json.Marshal, which would compact the raw body and break the
// hash). Time is writer-supplied but Append truncates it to whole UTC seconds so
// the RFC 3339 form is byte-stable across languages, and rejects an event older
// than the head (per-run monotonicity).
//
// Append is idempotent by Event.ID: re-appending an ID already committed returns
// the original event, making the at-least-once writer model safe.
package driverstate
