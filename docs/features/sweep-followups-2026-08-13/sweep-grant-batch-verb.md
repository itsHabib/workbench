**Status**: draft
**Owner**: @mh
**Date**: 2026-08-13
**Related**: dossier task `sweep-grant-batch-verb` (id: `tsk_01KZXTXQSMRQE6NZ5F72T1R1TY`)

# Grant sweep preflight: one command mints per-repo grants for every repo with open PRs — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | cmd/gate (new verb file + main.go wiring + grant plumbing) | ~180 | 180 |
| Tests | new verb tests | ~120 | 60 |
| **Total** | | | **~240** |

Band: **ideal**.

## Goal

Every stall in the 2026-08-12/13 PR burn-down (22 merges) was grant logistics: grants absent, expired mid-session (workbench/ship/drive), or cycle-capped (cc-skills#24 at 10 cycles, ship#235 at 5 vs the default `-max-cycles 3`). The operator hand-minted ~14 grants across three interruptions. One operator-run preflight should mint the sweep's whole grant set up front.

## Behavior

Add an operator-run preflight verb to the gate CLI (`gate grant -sweep` or a `gate sweep-mint` verb — pick the idiom that fits the existing flag surface) that:

1. Enumerates repos with open PRs (`gh search` across the owner, or an explicit `-repos` list).
2. For each, mints a grant sized for a sweep (tier/TTL/cycles from flags with sweep-appropriate defaults — cycles default should reflect the observed reality that sweeps burn more than `-max-cycles 3`).
3. Prints the minted set as a table (repo → grant id → tier → ttl → cycles).

Minting stays operator-only: this is a verb the OPERATOR runs once at sweep start; it must not weaken the "agents never mint" boundary (the permission layer already asks on `gate grant` — the new verb must fall under the same ask rule; name it so the existing `gate grant` prefix rule covers it if feasible).

## Acceptance

- Running the verb against a fixture of repos yields one grant per repo with the requested shape; table output lists them.
- A repo with no open PRs is skipped and reported as skipped.
- The verb path is covered by the same operator-only permission semantics as `gate grant` (documented in the verb's help text).

## Test plan

Fixture-driven: fake the repo enumeration seam, assert per-repo mint calls + skip behavior + table rendering. No live gh in tests.

## Non-goals

Automatic re-mint on expiry mid-sweep; agent-side minting of any kind.
