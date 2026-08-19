**Status**: draft
**Owner**: @mh
**Date**: 2026-08-19
**Related**: dossier task `flare-card-run-id` (id: `tsk_01M0DCXQXD0J97QSHDXSMJDDP8`), phase `escalation-live-2026-08-19`, [docs/features/escalation-plane/spec.md](../escalation-plane/spec.md) §8 step 1

# Paste-ready resolve line on flare's Slack card and `gate next` — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `cmd/flare/internal/notify/notify.go`, `cmd/gate/main.go` (next/inbox render) | ~60 | 60 |
| Tests | notify tests, gate next tests | ~100 | 50 |
| **Total** | | | **~110** |

Band: **amazing** per repo's PR sizing convention.

## Goal

The escalation-plane spec's adoption sequence (§8) names the merged-to-used gap
precisely: nothing the operator sees carries the escalation id or the resolve
command. The Slack card already shows the run id (`gatelog.go:75`); this slice
lands §8 step 1 — surface the paste-ready resolve line so the loop works today
(Slack page on the phone → paste one line in a terminal), independent of the
buttons/tunnel path.

## Behavior

On a parked-escalation event (the same predicate as `resolvablePark` —
`Kind == "escalation"` with a non-empty artifact id):

1. **flare Slack card** (`renderSlackMessage`, `cmd/flare/internal/notify/notify.go`):
   append a context block containing the verbatim line
   `escalate resolve -escalation <esc_id> -grant grt_… -decision <pass|block> -who … -why "…"`
   with the real escalation id substituted and the remaining placeholders left as
   placeholders. Rendering only — flare stays a pure sink (Amendment 3), no new
   state, no new event fields required beyond what the source already emits.
2. **`gate next` / inbox** (`cmd/gate`): print the same `escalate resolve …` line
   next to the existing `gate judge -run …` line for each open escalation.

Events that are not resolvable parks render exactly as today.

## Acceptance

- Parked-escalation Slack payload contains the resolve line verbatim with the
  real `esc_…` id.
- `gate next` output for an open escalation contains both the judge line
  (unchanged) and the new resolve line.
- Non-park events (verdict-with-escalate, cursor-alert) are unchanged.

## Test plan

- notify: golden/assertion cases for a park event with id (line present, id
  substituted) and an event without id (absent).
- gate: extend the existing `gate next` test to assert the resolve line.

## Non-goals

- Any change to `escalate` itself, button behavior, or `gate resolve` semantics.
- Authenticating `who` (§8 step 2) — separate slice.
