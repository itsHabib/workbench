**Status**: proposed
**Owner**: @codex:control-room
**Date**: 2026-07-13
**Related**: dossier task `control-room-real-composition` (id: `tsk_01KXDW31W2ZBY7SJ84SMCXJ2EF`), [`../spec.md`](../spec.md), Phase 4 adapter contracts

# Control Room real-mode composition and unattended-run observability — binding spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Composition and publication | `cmd/controlroom/internal/app/*.go` | ~300 | ~300 |
| CLI and web wiring | `cmd/controlroom/main.go`, `cmd/controlroom/internal/web/*` | ~140 | ~140 |
| Operator presentation | `cmd/controlroom/internal/model/*`, `policy/*`, `web/static/*` | ~120 | ~120 |
| Tests and smoke fixtures | matching `_test.go` / `testdata` | ~300 | ~150 |
| **Total** | | **~860** | **~710** |

Band: **stretch**. Phase 5 owns the single serialized cross-source seam; splitting it would create two temporary publishers or duplicate freshness policy.

## Goal

Turn the six isolated adapters into one storeless real-mode projection that remains truthful through partial failure, overlapping refreshes, slow enrichment, and long unattended runs. The UI must answer not merely “is it running?” but “what durable stage is it in, when did it last move, is it waiting or stuck, what failed, and what can the operator safely do next?”

## Architecture and ownership

- Add `internal/app` as the sole composition owner. Adapters continue to own protocol parsing; `model` owns JSON shape; `policy` owns deterministic liveness/attention; `web` owns transport/presentation. No producer store, sibling source code, or mutation verb may be read or called.
- Define narrow injected collector interfaces in `app`; production wiring adapts the six existing adapter result types. Tests use deterministic fakes and an injected clock. Dossier remains one long-lived adapter in `serve`; one-shot `snapshot` closes it after collection.
- Treat Ship, Dossier, GitHub, and optional Tower as core sources. Run them concurrently under the 15-second core deadline. Tracelens depends on the current Ship result; toolhealth is independent. Enrichers run under the 35-second diagnostic deadline after the core publish.
- Tower is supplemental only: its receipt is visible and its worktree facts may qualify exact repository+branch context, but it never overrides GitHub or Ship truth and never creates readiness or liveness by itself.

## Immutable generations

- The bootstrap snapshot is version 0 with one `loading` receipt per configured source. Every successful store increments a process-local version; published values are deep-copied/owned and never mutated after storage.
- Each collection receives an internal epoch distinct from the visible version. A core publish is accepted only for the current epoch. Each enricher may publish one later snapshot for that same epoch; completion order is irrelevant. A canceled or superseded epoch can never publish, even if its process ignores cancellation briefly.
- The core publish contains current core receipts plus `loading` diagnostic receipts. Prior Tracelens/toolhealth payloads may be retained only with `stale` receipts and stale flags while the new diagnostic result is pending. A successful or explicitly degraded current result replaces retained data. An unavailable result retains prior payload as stale when present; without retained payload it remains unavailable.
- A failed core source never removes another source's current payload. Its own prior successful payload may remain only under exactly one `stale` receipt; the current failed attempt is represented by the stale receipt's sanitized error code/message rather than a duplicate receipt. Policy therefore sees exactly one receipt per source and cannot mistake retained evidence for current truth.

## Refresh identity, joining, and cancellation

- `Refresh` validates `mode=demo|real` and `trigger=auto|manual`, computes the accepted SHA-256 identity from mode, trigger class, adapter names, absolute executable paths, argv, timeouts, GitHub scopes, Dossier corpus, and workspace root, then returns immediately with `202` semantics.
- An identical in-flight identity returns `joined` and its existing baseline. A different identity cancels the old epoch, starts a new one, and returns `started`. Presentation filters never enter the identity.
- Only a manual real refresh calls Dossier's `CollectManual`; automatic refresh uses `Collect`, preserving the owner's one-half-open-probe breaker contract. Joining happens above the adapter so concurrent identical manual requests cannot create extra source calls.
- Demo refresh remains a deterministic single publish. Real `snapshot --json` performs one bounded collection and waits for diagnostic settlement or deadline; it never starts a timer or durable cache.

## Cross-source joins and stale safety

- Compose source-local rows first, canonicalize stable ordering, derive the repository filter set, then call `policy.ApplyPolicy` once per publish using the injected clock.
- Exact joins only: Dossier task ID/slug to Ship task/spec; HTTPS artifact URL to GitHub PR URL; canonical repository plus branch to optional Tower rows. Do not title-match or guess repository ownership.
- Current means exactly one receipt in `ok|degraded`. A stale, loading, unavailable, duplicated, or absent supporting receipt suppresses urgent/actionable/waiting conclusions that depend on it. Retained tool friction may remain informational with `stale=true`; every retained panel produces `source.stale`.
- GitHub negative evidence already observed on a current degraded page remains usable under the Phase 2 policy; positive readiness continues to require complete detail. Composition must not weaken that distinction.

## Unattended-run operator contract

- Extend the presentation-owned `Run` model with a compact `OperatorState` and `NextAction`; preserve `UpdatedAt` as the authoritative last durable update and `Phase` as the owner-reported stage. Allowed operator states are `progressing`, `waiting`, `stalled`, `failed`, `done`, and `unknown`.
- Derive operator state only after current-source liveness policy:
  - active plus a recent durable update => `progressing`;
  - owner status/phase explicitly denotes review, approval, judgment, or another wait boundary => `waiting`;
  - `on_fire/stalled_active` => `stalled`;
  - terminal failed or retry loop => `failed`;
  - terminal success/done => `done`;
  - stale/absent owner evidence => `unknown`.
- `NextAction` is factual and non-mutating: monitor until the next durable update; inspect the named judgment/review boundary; inspect owner evidence and failure class before deciding whether to retry; revalidate a stale source; or no action for terminal success. Never claim “resume” or “retry” is safe unless a future owner contract explicitly supplies that capability.
- Run rows and drawers show status plus operator state, phase/current stage, relative age and exact last-update timestamp, failure class, evidence availability, and next action. A bare `running` label without recency is forbidden. Stale data is visually and textually qualified.

## CLI and HTTP wiring

- Support `serve --mode demo|real` and `snapshot --mode demo|real --json`. Real mode requires explicit workspace root, Dossier corpus, and one-to-four validated GitHub scopes; executable paths are explicit flags/config with PATH-name defaults. The canonical config fingerprint uses normalized absolute paths and sorted scopes.
- Keep loopback/Host/CSP/CSRF rules unchanged. `/api/v1/refresh` admits both modes and `manual|auto`; every POST still requires exact JSON content type, Origin, cookie, and `X-Controlroom-CSRF` token before any collection or breaker change.
- The browser polls until the baseline is exceeded and diagnostic receipts settle, then schedules the accepted 60-second automatic refresh. Manual and automatic refreshes remain distinct identities.

## Acceptance

- One unavailable, malformed, or timed-out source preserves healthy current panels and produces one honest receipt. Prior data is never silently current; stale evidence cannot produce a higher-consequence attention item.
- Identical refreshes join once; different identities cancel; a superseded core or enricher cannot publish. Tracelens and toolhealth can complete in either order without overwriting each other.
- Manual refresh is the only path to Dossier half-open probing. Real snapshot and serve modes use explicit configuration and available owner tools without direct store reads or producer writes.
- Every active unattended run exposes durable stage, exact/relative last update, progressing/waiting/stalled classification, terminal failure class where known, evidence, and a conservative next action. Unknown and stale owner facts remain visibly unknown/stale.

## Test plan

- Unit tests: bootstrap, monotonic versions, immutable copies, identity determinism, join/cancel, current-epoch gates, stale retention, exact joins, repository set, operator-state/next-action matrix, and no false retry/resume claim.
- Integration tests: injected Ship+Dossier+GitHub+Tower core results; each source failing independently; diagnostic completion permutations/timeouts; superseded late results; Dossier manual/auto dispatch; real-mode fake executable/MCP smoke.
- HTTP/browser tests: both modes and triggers, auto-refresh cadence, progressive generations, loading/stale/unavailable labels, unattended-run row/drawer fields, CSRF-before-collection, and no ambiguous running label.
- Run formatting, vet, lint, repository tests with coverage, build, current-head CI, canonical reviewer requests, digest, and review-coordinator GO.

## Non-goals

Producer mutation or dispatch; retry/resume controls; background daemon; durable cache; SSE/WebSockets; fuzzy joins; new producer contracts; Phase 6's full Playwright matrix, screenshots, runbook, or retrospective.
