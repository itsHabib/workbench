**Status**: draft
**Owner**: @mh
**Date**: 2026-08-13
**Related**: dossier task `sweep-next-shows-cycles` (id: `tsk_01KZXTY6MNR48683JZV826CQY0`)

# `gate next`: show cycles spent per open PR — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | cmd/gate (next/render path + cycle-count read from state) | ~80 | 80 |
| Tests | next-output tests | ~80 | 40 |
| **Total** | | | **~120** |

Band: **amazing**.

## Goal

`gate next` shows awaiting-judgment runs and grant expiries but not the cycle count a PR has already burned. During the 2026-08-13 sweep, ship#235 hit `grant_cycle_exceeded: cycle 5 exceeds grant ceiling 3` AFTER a full gate+judge round, forcing a re-mint and a duplicate judgment (run_be530cb4fb2f2bfe re-recorded as run_1d535a0ab6ac09cb). The coming refusal should be visible before an evaluation is spent.

## Behavior

- Surface the recorded cycle count in `gate next` output, per repo or per PR row (e.g. `cycles: 5`).
- Where a live grant exists, flag rows whose NEXT cycle would exceed that grant's ceiling — the "this will refuse" warning.
- Read-only: derives entirely from recorded runs + grant state; writes nothing.

## Acceptance

- A fixture with recorded cycles renders the count on the row.
- A row whose next cycle would exceed the live grant's ceiling carries a visible flag; a row within ceiling does not.
- No cycle data → row renders as today (no fabricated zero).

## Test plan

Golden/fixture tests on the next-rendering path with recorded-run + grant fixtures covering: under ceiling, at ceiling (next exceeds), no grant, no cycles.

## Non-goals

Changing cycle accounting or grant semantics; auto-minting anything.
