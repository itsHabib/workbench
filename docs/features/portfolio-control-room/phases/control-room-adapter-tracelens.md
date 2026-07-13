**Status**: accepted
**Owner**: @codex:control-room
**Date**: 2026-07-13
**Related**: dossier task `control-room-adapter-tracelens` (id: `tsk_01KXDPT2JA5TSC9T3GWK6A1082`), [`../spec.md`](../spec.md)

# Control Room bounded Tracelens adapter — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Production source | `cmd/controlroom/internal/adapters/tracelens/*.go` | ~75 | ~75 |
| Tests and fixtures | same package | ~80 | ~40 |
| **Total** | | **~155** | **~115** |

Band: **ideal** per the repository PR-sizing convention.

## Goal

Add bounded reliability context from Tracelens owner output without importing analysis code, exposing raw traces, or changing Ship's producer status when diagnosis fails.

## Behavior / fix

- Add only `cmd/controlroom/internal/adapters/tracelens`. The package may import but must not modify `cmd/controlroom/internal/model`. Export a source-local `Result` containing `[]model.Diagnosis` plus `model.SourceReceipt`; if a required shared model type is absent, stop and escalate rather than extending the locked package.
- Accept normalized Ship runs plus its current `model.SourceReceipt` and apply eligibility inside the adapter: the Ship receipt must be `ok`; the subject must be a workflow run rather than a driver container; its producer status must be terminal; trace evidence availability must be `available`; and `updated_at` must fall within the preceding 14 days relative to the injected clock. Sort eligible runs by `updated_at` descending then ID ascending, take at most five, and execute `tracelens ship -json <run-ref>` with a ten-second per-call bound. An ineligible input must never start a process.
- Normalize verdict, tier, dialect, findings/severity, evidence locus, repair text, and explicit token/cost/latency availability into existing diagnosis shapes. Optionally consume the configured owner `report` contract without creating a second cache.
- Unsupported input/dialect, malformed JSON, timeout, nonzero exit, and unavailable telemetry create qualified diagnosis/source receipts only. They never rewrite or reinterpret Ship status.
- Ignore additive JSON fields, sanitize typed errors, and prohibit raw trace, stderr, credential-like strings, usernames, and absolute operator paths from snapshot output.

## Acceptance

Healthy, empty, stale/nonterminal/driver/no-trace/incomplete-Ship inputs, unsupported input/dialect, malformed, timeout, nonzero-exit, unavailable-telemetry, and mixed-run fixtures yield deterministic diagnoses. The eligibility gate, five-run cap, and ordering are exact, telemetry unknown never renders as zero, and failures remain source-local.

## Test plan

Use a fake executable and injected clock. Assert argv, eligibility/order/cap, cancellation, JSON tolerance, evidence/path safety, availability mapping, status independence, and sanitization. Run the full Go gates.

## Non-goals

On-demand diagnosis HTTP routes, durable caching, enrichment coordination, Tracelens owner changes, or shared adapter helpers outside this source directory.
