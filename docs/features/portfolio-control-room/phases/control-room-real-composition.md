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
| Tests and smoke fixtures | matching `_test.go` / `testdata` | ~280 | ~140 |
| **Total** | | **~840** | **~700** |

Band: **stretch**. The test estimate is intentionally tight and the total sits at the accepted Phase 5 ceiling. Phase 5 owns the single serialized cross-source seam; splitting it would create two temporary publishers or duplicate freshness policy, so the explicit 500-production-LOC checkpoint below is the reviewable escape hatch.

## Goal

Turn the six isolated adapters into one storeless real-mode projection that remains truthful through partial failure, overlapping refreshes, slow enrichment, and long unattended runs. The UI must answer not merely “is it running?” but “what durable stage is it in, when did it last move, is it waiting or stuck, what failed, and what can the operator safely do next?”

## Architecture and ownership

- Add `internal/app` as the sole composition owner. Adapters continue to own protocol parsing; `model` owns JSON shape; `policy` owns deterministic liveness/attention; `web` owns transport/presentation. No producer store, sibling source code, or mutation verb may be read or called.
- Define narrow injected collector interfaces in `app`; production wiring adapts the six existing adapter result types. Tests use deterministic fakes and an injected clock. Dossier remains one long-lived adapter in `serve`; one-shot `snapshot` closes it after collection.
- Treat Ship, Dossier, GitHub, and optional Tower as core sources. Run them concurrently under the 15-second core deadline. Tracelens depends on the current Ship result; toolhealth is independent. Enrichers run under the 35-second diagnostic deadline after the core publish.
- Tower is supplemental only: its receipt is visible and its worktree facts may qualify exact repository+branch context, but it never overrides GitHub or Ship truth and never creates readiness or liveness by itself.

## Immutable generations

- The bootstrap snapshot is version 0 with one `loading` receipt per configured source. Every successful store increments a process-local version; published values are deep-copied/owned and never mutated after storage.
- Each collection receives an internal monotonically increasing epoch distinct from the visible version. One coordinator mutex owns `activeEpoch`, `inFlight`, the accepted-enricher set, the version counter, and publication. Under that mutex, a store first compares its epoch with `activeEpoch`; only an accepted store increments version and swaps the snapshot. Rejected/canceled attempts neither publish nor consume a version, so visible skips cannot be caused by superseded work.
- The coordinator records `inFlight` and the epoch's fixed `baselineVersion` under the mutex before dispatching any collector. Each enricher produces at most one buffered result per epoch; duplicate/retry results are dropped. After the core publish, one enrichment coordinator uses `sync.WaitGroup` plus buffered per-enricher result channels to rendezvous Tracelens and toolhealth or the 35-second deadline, then performs exactly one accepted `atomic.Pointer[Snapshot].Store` that overlays both settled results and stale-retains any timed-out result, provided the epoch token is still current. Completion order is irrelevant and the browser never observes a half-enriched intermediate store.
- The core publish contains current core receipts plus `loading` diagnostic receipts. If a diagnostic payload was previously current, the core publish carries both a `loading` current-attempt receipt and a `stale` retained-data receipt, matching the accepted TDD. A settled unavailable result likewise carries the unavailable attempt receipt plus the retained stale receipt/payload. Success or explicit degradation replaces retained data and emits only the current receipt.
- A failed core source never removes another source's current payload. For that source, a current `unavailable` receipt is always emitted. A retained payload adds one separate `stale` receipt drawn only from the coordinator's `lastCurrent[source]` cache—the most recent accepted `ok|degraded` payload—not from an earlier stale snapshot. A source that has never succeeded in this process emits no stale receipt. Repeated failures cannot refresh or flatten the retained payload's original `observed_at`.
- Receipt grouping is structural, not a final dedup guess: publication builds a mutex-local `map[source][]SourceReceipt`, permits at most one current-attempt receipt and one retained-stale receipt, then flattens sources in stable source/state order. `policy.ApplyPolicy` indexes the grouped states: a source is current only when it has one `ok|degraded` receipt and no stale/unavailable/loading receipt; retained+failed sources produce both `source.unavailable` and `source.stale` informational items and no higher-consequence conclusion.
- Deep ownership is tested in both directions: mutate every nested slice in a fake collector result after collection, and mutate every nested slice in a snapshot returned by the supplier. Neither mutation may alter the subsequently returned/published snapshot. The store deep-clones on ingress and the supplier deep-clones on egress.

## Refresh identity, joining, and cancellation

- `Refresh` validates `mode=demo|real` and `trigger=auto|manual`, computes the accepted SHA-256 identity from mode, trigger class, stable source IDs, absolute executable paths, argv, timeouts, sorted GitHub scopes, Dossier corpus, and workspace root, then returns immediately with `202` semantics. Stable source IDs (`ship`, `dossier`, and so on) identify result ownership and are independent of executable basenames; paths identify the actual configured binary, so both belong in the identity.
- The coordinator mutex makes check/join/cancel/start atomic. An identical in-flight identity returns `joined` and the epoch's fixed `baselineVersion` captured before any of its publishes—not the version at join time. A different identity cancels the old epoch, records the new epoch/baseline, then dispatches collectors and returns `started`. Presentation filters never enter the identity.
- Trigger class intentionally prevents auto/manual joining because they invoke different Dossier behavior. A manual request supersedes an in-flight auto epoch; a different-identity auto request can supersede manual, but the browser never schedules its next automatic request until the manual epoch's diagnostics settle or time out. This preserves the accepted “different identity cancels” TDD rule without a timer routinely interrupting operator probes.
- Only a manual real refresh calls Dossier's `CollectManual`; automatic refresh uses `Collect`. The inherited owner contract is: after three consecutive start/handshake/first-call failure cycles, automatic probes pause for five minutes; one concurrent manual half-open probe is admitted; one failed manual probe counts as one failed cycle and reopens the five-minute pause; success resets the counter. Composition neither reads nor modifies breaker state. Epoch recording precedes adapter dispatch, so concurrent identical manual requests join before any Dossier call can start.
- Demo refresh remains a deterministic single publish. Real `snapshot --json` performs one bounded collection and waits for diagnostic settlement or deadline; it never starts a timer or durable cache.

## Cross-source joins and stale safety

- Compose source-local rows first, canonicalize stable ordering, derive the repository filter set, then call `policy.ApplyPolicy` once per publish using the injected clock.
- Exact joins only: Dossier task ID/slug to Ship task/spec; HTTPS artifact URL to GitHub PR URL; canonical repository plus branch to optional Tower rows. Do not title-match or guess repository ownership.
- Current means exactly one receipt in `ok|degraded`. A stale, loading, unavailable, duplicated, or absent supporting receipt suppresses urgent/actionable/waiting conclusions that depend on it. Retained tool friction may remain informational with `stale=true`; every retained panel produces `source.stale`.
- GitHub negative evidence already observed on a current degraded page remains usable under the Phase 2 policy; positive readiness continues to require complete detail. Composition must not weaken that distinction.
- “No direct store reads” means no reads of Ship, Dossier, Tower, or other producer backing files/databases. Composition is explicitly allowed to read and join the normalized payloads returned by adapter interfaces.
- Error code/message handling remains adapter-owned: Phase 4's typed, bounded, credential/path-sanitized receipts pass through unchanged. Composition may replace a message only with a fixed operator-safe composition error and must never append raw subprocess/protocol text. `NextAction` branches on typed state/code allowlists, never arbitrary message substrings.

## Unattended-run operator contract

- Extend the presentation-owned `Run` model with a compact `OperatorState` and `NextAction`; preserve `UpdatedAt` as Ship's authoritative last durable owner update and `Phase` as its owner-reported stage. When Ship becomes stale, retained runs keep the exact last Ship-current `UpdatedAt`; composition never replaces it with a snapshot publication time. Allowed operator states are `progressing`, `waiting`, `stalled`, `failed`, `done`, and `unknown`.
- Re-derive operator state from scratch on every publish; never carry a prior derived state forward. Evaluate this exclusive precedence chain after liveness policy: (1) stale/loading/unavailable Ship support => `unknown`; (2) a run that is itself terminal `failed|error|cancelled` => `failed`, including when that failed run's normalized document identity also satisfies `on_fire/retry_loop`; (3) terminal `done|completed|succeeded|success` => `done` regardless of whether older runs for the same document form a retry-loop group; (4) `on_fire/stalled_active` => `stalled`; (5) status or phase equal-fold matches the explicit wait allowlist `waiting|awaiting_review|review|approval|judgment|blocked` => `waiting`; (6) active `pending|running|dispatching|dispatched` with `UpdatedAt` newer than the Phase 2 15-minute no-update threshold => `progressing`; otherwise => `unknown`. Exact normalized tokens are used—no substring/keyword guessing.
- `NextAction` is factual and non-mutating: monitor until the next durable update; inspect the named judgment/review boundary; inspect owner evidence and failure class before deciding whether to retry; revalidate a stale source; or no action for terminal success. Never claim “resume” or “retry” is safe unless a future owner contract explicitly supplies that capability.
- Map `NextAction` from the final exclusive state: `progressing` => monitor until the next durable owner update; `waiting` => inspect the named owner wait boundary; `stalled` => inspect owner evidence for stall signals; `failed` => inspect owner evidence and failure class before deciding whether to retry; `done` => no action; `unknown` => revalidate Ship/source truth. The 15-minute transition prevents an indefinitely unchanged run from retaining `progressing`.
- Run rows and drawers show status plus operator state, phase/current stage, relative age and exact last-update timestamp, failure class, evidence availability, and next action. A bare `running` label without recency is forbidden. Stale data is visually and textually qualified.

## CLI and HTTP wiring

- Support `serve --mode demo|real` and `snapshot --mode demo|real --json`. Real mode requires explicit workspace root, Dossier corpus, and one-to-four validated GitHub scopes; executable paths are explicit flags/config with PATH-name defaults. The canonical config fingerprint uses normalized absolute paths and sorted scopes.
- Keep loopback/Host/CSP/CSRF rules unchanged. `/api/v1/refresh` admits both modes and `manual|auto`; every POST still requires exact JSON content type, Origin, cookie, and the accepted `X-Control-Room-CSRF` token before any collection or breaker change.
- The browser polls until the baseline is exceeded and diagnostic receipts settle, then schedules the accepted 60-second automatic refresh. Manual and automatic refreshes remain distinct identities.

## Acceptance

- One unavailable, malformed, or timed-out source preserves healthy current panels and produces one honest receipt. Prior data is never silently current; stale evidence cannot produce a higher-consequence attention item.
- Identical refreshes join once; different identities cancel; a superseded core or enricher cannot publish. Tracelens and toolhealth can complete in either order without overwriting each other.
- Manual refresh is the only path to Dossier half-open probing. Real snapshot and serve modes use explicit configuration and available owner tools without direct store reads or producer writes.
- Every active unattended run exposes durable stage, exact/relative last update, progressing/waiting/stalled classification, terminal failure class where known, evidence, and a conservative next action. Unknown and stale owner facts remain visibly unknown/stale.
- If non-test `internal/app` composition exceeds 500 LOC at the checkpoint before CLI/web wiring, stop and split diagnostic-enricher settlement into a second serialized PR. The first PR must still publish core snapshots with truthful `loading` plus retained-stale diagnostic receipts; no stale or generation edge case may be cut to stay in one PR.

## Test plan

- Unit tests: bootstrap, accepted-store-only monotonic versions, nested immutable copies, identity determinism, mutex-atomic join/cancel, fixed joined baseline, current-epoch and one-result-per-enricher gates, exactly one rendezvoused enrichment store, never-succeeded/repeated-failure stale provenance, dual receipt policy, exact joins, repository set, exclusive operator-state/next-action matrix, and no false retry/resume claim.
- Integration tests: injected Ship+Dossier+GitHub+Tower core results; each source failing independently; diagnostic completion permutations/timeouts; superseded late results; Dossier manual/auto dispatch; real-mode fake executable/MCP smoke.
- HTTP/browser tests: both modes and triggers, auto-refresh cadence, progressive generations, loading/stale/unavailable labels, unattended-run row/drawer fields, CSRF-before-collection, and no ambiguous running label.
- Run formatting, vet, lint, repository tests with coverage, build, current-head CI, canonical reviewer requests, digest, and review-coordinator GO.

## Non-goals

Producer mutation or dispatch; retry/resume controls; background daemon; durable cache; SSE/WebSockets; fuzzy joins; new producer contracts; Phase 6's full Playwright matrix, screenshots, runbook, or retrospective.
