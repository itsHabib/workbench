**Status**: draft
**Owner**: @michael
**Date**: 2026-07-13
**Related**: dossier task `flare-lift-park-receipts` (id: `tsk_01KXDYH4X6KVWJ7357ZBSNGAK1`, workbench project, offload-adoption phase). Cross-repo producer: ship `emit-park-receipts` (`tsk_01KXDYHV5Y3EP1C9Z3WKJ1ADKJ`) — must land first; outcome string pinned to `parked`.

# Flare: lift ship park receipts as page-worthy events — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `cmd/flare/internal/source/receipts.go` (add a `parked` case) | ~5 | 5 |
| Tests | `cmd/flare/internal/source/source_test.go` (extend the receipts table) | ~20 | 10 |
| **Total** | | | **~15** |

Band: **amazing** — a one-case switch extension, table-test-pinned.

## Goal

`receiptSeverity` (`cmd/flare/internal/source/receipts.go`) lifts only `failed` /
`cancelled`; every other outcome is dropped at the source (the `default` returns `(0, false)`
so `receiptEvent` skips it). Driver parks (`awaiting_judgment`) — the single most page-worthy
event for the operator-attention story, "the engine stops and asks for my judgment" — never produce a
notification, even once ship emits them (ship `emit-park-receipts`, outcome pinned to
`parked`). This is the flare half of the push-on-block gap named in
`cmd/flare/docs/FOLLOWUPS.md` ask #1.

## Behavior / fix

- Add `case "parked": return event.SevEscalate, true` to `receiptSeverity`. **`SevEscalate`
  is the exact band**: its doc comment is literally "a producer parked for judgment," and the
  gate log's `parked_for_judgment` escalation already maps there — a ship park is the
  ship-side analogue. It is page-worthy and strictly above `SevFailed`, satisfying the task's
  "at least as severe as `failed`."
- **Do not touch the `default` arm.** Still-unknown outcomes (`succeeded` / `merged` /
  `pending` / anything new) keep returning `(0, false)` and lift no event — unchanged from
  today. The "non-dropping catch-all" posture is a **route-layer** invariant (an event that
  matches no route goes to the catch-all channel); the **source** layer deliberately lifts
  only page-worthy outcomes, and that stays exactly as-is for everything but `parked`.
- Dedupe ID stays `key + ":" + outcome` (the existing `receiptEvent` rule) — a run that parks
  (`<key>:parked`) then later fails (`<key>:failed`) pages twice: different facts, correct.

## Acceptance

- A receipts fixture line with `outcome: "parked"` lifts exactly one event with
  `Severity == event.SevEscalate` and dedupe ID `<key>:parked`.
- `failed` / `cancelled` still lift (`SevFailed` / `SevCancelled`); `succeeded` / `merged` /
  `pending` / an unknown outcome still lift nothing — no new silent drops and no new lifts.
- `go test ./cmd/flare/...` green; `gofmt -l`, `go vet ./...`, `golangci-lint run ./...` clean.

## Test plan

Extend `TestReceiptsLiftFailedAndCancelledOnly` (or add a sibling) to include `parked` in the
outcome set and assert: the lifted count is `failed` + `cancelled` + `parked`; the parked
event's `Severity` is `SevEscalate` and its ID is `wf_parked:parked`; the other outcomes still
lift nothing. Rename the test if its name now under-describes what it pins.

## Non-goals

- The ship-side emission (ship `emit-park-receipts`).
- New channels, or a routes-table `parked` entry — `SevEscalate` already routes via the
  existing severity-based routing (gate escalations use it); add a route only if a test shows
  a gap.
- Changing dedupe / throttle semantics or the cursor-integrity path.
