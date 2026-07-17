**Status**: draft
**Owner**: @mh
**Date**: 2026-07-16
**Related**: dossier task `driver-state-ledger-read` (id: `tsk_01KXPXDD23GBK4HR33JNSETK0D`), locked design [docs/features/driver-state/spec.md](../driver-state/spec.md) §6–8 (PR #47)

# driverstate ledger read path: Reduce/Runs/Verify — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | driverstate/reduce.go, driverstate/runs.go, driverstate/verify.go | ~250 | 250 |
| Tests | driverstate/*_test.go (read path) | ~350 | 175 |
| **Total** | | | **~425** |

Band: **amazing** per workbench PR sizing.

## Goal

The read half of the `driverstate/` package: fold a hash-chained ledger back into `RunState`, list runs tolerantly, verify chain integrity. Depends on the write-path PR (same phase).

## Behavior

Per spec §6 (the locked contract):

```go
func Reduce(dir, run string) (RunState, error)  // pure fold; unknown kinds tolerated; chain break = error
func Runs(dir string) ([]RunSummary, error)     // never hard-fails on one bad run (tolerant listing)
func Verify(dir, run string) error              // chain integrity
```

- **Reduce** is a pure fold. **Manifest-seeded streams**: initialize `RunState.Streams` from `run_imported.Body.streams` and overlay statuses from subsequent events — a stream with no events yet reads `pending`, so a resuming session always sees the full stream set.
- Unknown event kinds: skip-with-warning (schema tolerance, spec §8).
- Chain break **mid-file** = `ErrChainBroken`, loud — never silently truncate (a swallowed `stream_merged` would re-drive a merged PR). A torn **final** line is discarded with a warning (readers are lock-free; the writer may be mid-append).
- **Runs** never hard-fails on one bad run: a broken chain flags that row `status: corrupt` and every other run still lists (spec §7 F5 — the direct fix for ship's `driver list` grok-4.5 failure class).
- **Verify** returns ok or `ErrChainBroken` detail.

## Acceptance

- Reduce over the P1 reference-vector ledger yields the pinned `RunState`.
- Spec §7 F3 (manifest-seeded resume view) and F5 (tolerant listing over a corrupt run) reproduced in tests.
- CI hygiene green.

## Test plan

- Reduce: manifest-seeded pending streams; status overlay per kind; unknown-kind skip-with-warning; mid-chain break error; torn-final-line discard.
- Runs: multi-run dir with one corrupt run flagged, others listed.
- Verify: ok + broken-chain detail.

## Non-goals

- Write path (prior PR in this phase).
- MCP server / CLI (P3).
