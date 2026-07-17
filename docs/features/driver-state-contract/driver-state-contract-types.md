**Status**: draft
**Owner**: @mh
**Date**: 2026-07-16
**Related**: dossier task `driver-state-contract-types` (id: `tsk_01KXP12DY542TK0JMB5EG14KDY`), locked design [docs/features/driver-state/spec.md](../driver-state/spec.md) §5–6 (PR #47)

# contracts/driverstate: event types + schema + conformance tests — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | contracts/driverstate/*.go + embedded JSON schema | ~350 | 350 |
| Tests | contracts/driverstate/conformance_test.go (+ vector fixture) | ~400 | 200 |
| **Total** | | | **~550** |

Band: **ideal** per workbench PR sizing.

## Goal

Give driver-state events (spec §5) their contract home. The package lives in `contracts/` as a leaf — types + embedded JSON schema, zero decision logic — per the workbench boundary law, mirroring the `contracts` verdict-v0.3.0 pattern.

## Behavior

New `contracts/driverstate` package:

- `Event{ID, Run, V, Kind, Stream, Time, Actor, Body, Prev, Hash}`.
- Kind consts: `run_imported`, `stream_dispatched`, `stream_attempt`, `stream_pr_opened`, `stream_landed`, `stream_failed`, `stream_skipped`, `stream_merged`, `run_finished`.
- Kind-specific body payload types.
- `RunState` / `RunRecord` / `StreamRecord` (reducer *output shapes* only — no reducer here).
- Embedded `driver-state-v0.1.0` JSON schema.
- Tolerant reader: unknown kinds are skipped with a warning, never an error.
- **Canonical-encoding reference test vector** (spec §5, review M3): a pinned fixture asserting the canonical byte encoding used for the hash chain — this is a P1 deliverable, not optional.
- A body-level `ship_run_ref` string field where the spec calls for it (spec §10 Q2 — include the field, defer semantics).

**NO `Append`/`Reduce`/locking/MCP here** — that is P2/P3 mechanism. `contracts/` stays a leaf (imports stdlib only).

The locked design is the contract: `docs/features/driver-state/spec.md` §5 (event schema + canonical encoding) and §6 (state machine / reducer shapes). Do not relitigate D1–D6 or resolved §10 questions.

## Acceptance

Conformance trio passes, mirroring `contracts/` verdict tests:
1. Schema ↔ Go field/required parity.
2. Enum ↔ const parity.
3. Tolerant-reader round-trip.

CI hygiene job still green (contracts stays a leaf).

## Test plan

`conformance_test.go`:
- Parity tests generated the same way as verdict-v0.3.0's.
- Tolerant reader over a ledger containing an unknown future kind.
- Payload validation per kind (e.g. bad `stream_attempt` seq rejected at unmarshal-validate).
- Canonical-encoding reference vector: fixed event → pinned canonical bytes → pinned hash.

## Non-goals

- Append/Reduce/lease/lock mechanism (P2).
- MCP surface (P3).
- ship correlation semantics beyond the `ship_run_ref` string field.
