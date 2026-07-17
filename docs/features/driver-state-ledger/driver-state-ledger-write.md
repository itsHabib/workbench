**Status**: draft
**Owner**: @mh
**Date**: 2026-07-16
**Related**: dossier task `driver-state-ledger-write` (id: `tsk_01KXPXCV2S85AYYA735GSRZXBS`), locked design [docs/features/driver-state/spec.md](../driver-state/spec.md) §6–8 (PR #47)

# driverstate ledger write path: lease + Append — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | driverstate/lease.go, driverstate/append.go, driverstate/errors.go | ~350 | 350 |
| Tests | driverstate/*_test.go (lease + append) | ~450 | 225 |
| **Total** | | | **~575** |

Band: **ideal** per workbench PR sizing.

## Goal

The mechanism half of the driver-state plane's write side: durable single-writer-per-run leases and hash-chained, crash-safe appends. New top-level `driverstate/` package — shared mechanism, imports at most `contracts` (leaf-checked like `local/`).

## Behavior

Per spec §6 (the locked contract — do not relitigate D1–D6):

```go
func Claim(dir, run, actor string) (Lease, error)     // durable run ownership; ErrLocked{Holder} if held
func (l Lease) Renew() error                          // heartbeat; stale = expired lease, not just PID
func (l Lease) Release() error
func Append(dir string, l Lease, e Event) (Event, error) // requires a live lease; idempotent by e.ID
```

- **Lease** file carries `{actor, pid, expires_at}`; staleness = expiry (inherit gate's threshold as the default, configurable). A killed session's lease self-clears within one threshold window. Reuse gate's Windows delete-pending → retry-everything lock lesson.
- **Append** semantics (review-nailed): acquire the append lock → truncate any torn tail to the last verified newline → read the head inside the lock (chain, time-monotonicity, and `stream_attempt` seq validation all use this read) → write + fsync → release.
- The run *lease* enforces single-writer-per-run across a session; the per-append lock only prevents byte races.
- Idempotent by `e.ID`: a retried append returns the original committed event.
- Writer-supplied time, append-enforced monotonicity per run (reject an event older than the head).
- State-machine validation: recording an illegal transition (spec §7 F2, e.g. `stream_merged` on a stream at `dispatched` with no `pr_opened`) rejects with `ErrIllegalTransition{From, Event}`.
- Errors are values with stable codes: `ErrIllegalTransition`, `ErrChainBroken`, `ErrLocked`. No panics, no silent skips on write paths.
- Canonical JSON encoding + hash rule: use `contracts/driverstate`'s canonical encoding (P1 reference vector is the pinned truth) and document the chain rule in the package doc — ship's TS emitter (P5) implements it independently, so keep it dead simple.

## Acceptance

- Second claimer on a live lease fails fast with `ErrLocked{Holder}` naming the holder (spec §7 F4).
- Crash-torn tail is truncated cleanly on the next append.
- Illegal transitions rejected per the state machine.
- Idempotent re-append returns the original event.
- Appended chain verifies against the P1 canonical-encoding reference vector.
- CI hygiene green: `driverstate` imports at most `contracts`.

## Test plan

- Lease: claim/renew/release; stale-expiry self-clear; second-claimer `ErrLocked`; Windows delete-pending retry.
- Append: torn-tail truncation fixture; monotonicity rejection; illegal-transition matrix; idempotent retry; fsync'd chain matches the reference vector.

## Non-goals

- `Reduce`/`Runs`/`Verify` (read-path PR, next in this phase).
- MCP server / CLI (P3).
- ship TS emitter (P5).
