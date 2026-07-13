**Status**: draft
**Owner**: @codex:control-room
**Date**: 2026-07-13
**Related**: dossier task `control-room-adapter-toolhealth` (id: `tsk_01KXDPT2JW0F4TSGH6SFM8Q9TQ`), [`../spec.md`](../spec.md)

# Control Room tolerant tool-health adapter — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Production source | `cmd/controlroom/internal/adapters/toolhealth/*.go` | ~70 | ~70 |
| Golden text tests | same package | ~90 | ~45 |
| **Total** | | **~160** | **~115** |

Band: **ideal** per the repository PR-sizing convention.

## Goal

Consume the existing tool-health friction rollup without duplicating its local-model classification, while treating its current human-oriented text surface as a narrow tolerant boundary.

## Behavior / fix

- Add only `cmd/controlroom/internal/adapters/toolhealth`. Execute the configured `toolhealth.exe` board command once per diagnostic collection with a bounded context.
- Parse only fixture-backed fields: tool, worst severity, recurrence/session count, last occurrence, pain lines, and accumulated-friction label. Accept additive lines and reordered sections; missing optional values remain unknown.
- Missing executable, timeout, nonzero exit, or loss of the required text anchors produces a sanitized degraded/unavailable receipt. Retain parseable rows only when the response remains unambiguous; never turn absence into zeros.
- Do not invoke Ollama/local-model classification, read the friction store, or reproduce tool-health bucketing in Control Room.

## Acceptance

Healthy, empty, additive, reordered, missing-optional, contract-drift, timeout, nonzero-exit, and executable-not-found fixtures fail independently and preserve every unambiguous row honestly.

## Test plan

Use a fake executable and golden text fixtures. Assert exact argv, tolerant parse boundaries, unknown-vs-zero behavior, stable ordering, drift detection, sanitization, and absence of local-model/store calls. Run the full Go gates.

## Non-goals

Owner-side JSON work, refresh coordination, stale retention, UI changes, new friction classification, or shared adapter helpers outside this source directory.
