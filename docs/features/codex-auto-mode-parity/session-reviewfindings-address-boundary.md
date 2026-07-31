**Status**: draft
**Owner**: @codex:michael
**Date**: 2026-07-29
**Related**: dossier task `session-reviewfindings-address-boundary` (`tsk_01KYP81VP40DX668F3G6JCCKGR`)

# Session-native review-findings address boundary

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Production | review-findings contracts/commands plus `driverstate/` | ~360 | 360 |
| Tests/fixtures | refusal, crash, and shared conformance scenarios | ~600 | 300 |
| **Total** | | | **~660** |

Band: **ideal**.

## Goal

Accept and consume an exact-head `ReviewFindingsV1` for a native session-engine
stream without invoking Ship or any provider SDK, then emit one reconstructable
address work item for a fresh isolated Codex child.

## Behavior / fix

- Make the original implementation **child ledger** authoritative for address
  cycles and work state; the parent retains only its coarse mirror.
- Validate supported major, exact live PR head, non-empty sourced address
  findings, source/panel consistency, remaining engine-owned cycle capacity,
  and unused artifact id/digest.
- Introduce driver-state v0.2 for these authority-bearing event kinds:
  `review_address_prepared` (atomic consumption/outbox),
  `review_address_claimed` (address child-run link),
  `review_address_started` (Codex task/thread id), and
  `review_address_completed` (new PR head).
- The new reader accepts v0.1 and v0.2 events in one unchanged hash chain and
  reduces both; it never rewrites historical lines. The first address event is
  v0.2, so every already-installed v0.1 writer hits its existing
  unknown-version gate while reading the ledger and refuses before append.
  Tests pin this old-writer exclusion boundary.
- Ship neither emits nor reads these session-only ledgers. Its v0.1 emitter is
  unchanged; any future Ship writer for a session ledger must add v0.2 support
  first rather than relying on unknown-kind tolerance.
- `driverstate.PrepareReviewAddress` appends under the authoritative child
  lease after policy validation. Its body contains scalar refs/digests only;
  `contracts/driverstate` never imports `contracts/reviewfindings`.
- Put `AddressWorkV1` in `contracts/reviewfindings`; the ledger stores its
  path/ref and digest. Its deterministic work id is derived from child
  run+stream+artifact digest+cycle.
- Persist the bounded work item before the ledger append at a deterministic,
  state-root-confined path. Write a same-directory temporary file, sync it,
  atomically rename it into place, and sync the directory where supported.
  Retry verifies identical bytes/digest before reuse and refuses a collision.
  A crash before the ledger append leaves an inert deterministic orphan that a
  retry adopts; a crash after append is reconstructable from the stored
  ref/digest. Cleanup never guesses or silently deletes an orphan.
- Before external spawn, import the address child with a deterministic import
  key and append `review_address_claimed`. After task creation returns, append
  `review_address_started`.
- Refuse stale, malformed, duplicate, exhausted, empty/unsourced, and
  inconsistent input before child dispatch.
- Distinguish duplicate submission from recovery: duplicate `accept` refuses
  and names the existing work ref; `resume --work <id>` reconstructs it.
- On resume, adopt by work id/task id and reconcile GitHub. A crash between
  external spawn and recording its id is ambiguous and **parks** rather than
  automatically spawning a second child. If the original worktree still owns
  the PR branch, adopt/handoff it or park instead of creating a conflicting
  checkout.
- Define the canonical cross-consumer scenarios at
  `contracts/reviewfindings/testdata/address-v1/`. Each case carries artifact
  bytes, live head/cycle/consumed-id state, ordered accept/resume calls, expected
  refusal code, and a common accept/consumption/at-most-once projection. It then
  carries consumer-specific effects: Workbench address-work/claim state and
  Ship provider-call count. A manifest pins every case digest and the corpus
  digest.

## Acceptance

One exact-head artifact is consumed once and creates one address work item.
Reducer state exposes pending/claimed/started/completed. Crash tests cover each
file-write/rename/event boundary and each subsequent event boundary; ambiguity
parks and never auto-duplicates. Workbench executes every canonical scenario
before Ship vendors it. A fresh Codex child updates the existing PR using
normal GitHub connector or `gh`/git authentication, without Ship cloud dispatch
or a model-provider SDK/API key.

## Test plan

Generated valid/invalid artifacts, the canonical ordered conformance corpus,
bounded repeated-consumption sequences, lease/reducer/file-crash transition
tests, a real GitHub-head fixture, full Workbench checks, and hygiene.

## Non-goals

No Ship/provider dispatch, coordinator logic, Gate policy, Claude invocation,
API key, or generic cross-engine workflow framework.
