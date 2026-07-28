**Status:** draft
**Owner:** @itsHabib
**Date:** 2026-07-28
**Related:** Dossier task `emit-codex-closure-receipt` (`tsk_01KYMQHPWFTNASQ68N7PDYSB9F`)

# Emit Codex closure receipt facts from Ship

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Ship driver emission | driverstate emitter plus narrow engine/store call sites | ~180 | 180 |
| Tests | emitter/engine/store sequences | ~260 | 130 |
| Phase doc | Ship-scoped implementation record | ~60 | 30 |
| **Total** | | | **~340** |

Band: **ideal**.

## Goal

Adopt Workbench's merged receipt-contract extension and emit authoritative facts
from Ship's existing address, review, Gate-handoff, and land path.

## Behavior

- Emit facts already known at ReviewFindingsV1 consumption, address dispatch and
  result, Gate handoff, and merge readback.
- Persist the typed producer catalog revision from the consumed
  `ReviewFindingsV1`; never reconstruct it from an installed path, producer id,
  or current checkout.
- Preserve transactional at-most-once consumption and address-time stale-head
  checks.
- Record explicit failures/interventions; never infer judgment or panel
  completeness.
- Narrowly adopt the optional typed catalog-revision field in Ship's existing
  ReviewFindingsV1 parser; do not redesign parsing or add a receipt database.

## Acceptance

- A fake address → new head → review → Gate ref → land sequence reconstructs one
  complete receipt.
- Missing catalog revision accepts legacy review input but keeps the closure
  receipt explicitly incomplete; malformed provenance refuses at schema
  validation.
- Duplicate address/land calls do not duplicate terminal closure.
- Stale/refused paths produce typed failure/intervention facts and no false
  completion.
- Existing parser/address tests remain green.

## Test plan

- Focused driver-state emitter, engine, and store tests.
- `make check`.

## Non-goals

Native skill implementation, broad parser redesign, or receipt analytics.
