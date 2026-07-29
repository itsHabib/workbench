**Status**: draft
**Owner**: @codex:michael
**Date**: 2026-07-29
**Related**: dossier task `session-reviewfindings-address-boundary` (`tsk_01KYP81VP40DX668F3G6JCCKGR`)

# Session-native review-findings address boundary

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Production | review-findings and driver-state contracts/commands | ~320 | 320 |
| Tests | generated refusal/dedupe/state sequences | ~500 | 250 |
| **Total** | | | **~570** |

Band: **ideal**.

## Goal

Accept and consume an exact-head `ReviewFindingsV1` for a native session-engine
stream without invoking Ship or any provider SDK, then emit one reconstructable
address work item for a fresh isolated Codex child.

## Behavior / fix

- Extend the existing Workbench review-findings and driver-state surfaces.
- Validate supported major, exact live PR head, non-empty sourced address
  findings, source/panel consistency, remaining engine-owned cycle capacity,
  and unused artifact id/digest.
- Atomically record artifact id, digest, head, and assigned cycle against the
  session parent/child stream.
- Emit `AddressWorkV1` naming the existing PR/branch and bounded findings; the
  session orchestrator delegates it to a fresh child.
- Refuse stale, malformed, duplicate, exhausted, empty/unsourced, and
  inconsistent input before child dispatch.
- Preserve at-most-once child creation across crash/retry.

## Acceptance

One exact-head artifact is consumed once and creates one address work item.
Every refusal code matches the shared conformance corpus, and a fresh Codex
child updates the existing PR branch without cloud or API credentials.

## Test plan

Generated valid/invalid artifacts, bounded repeated-consumption sequences,
driver-state transition/conformance tests, a real GitHub-head fixture, full
Workbench checks, and hygiene.

## Non-goals

No Ship/provider dispatch, coordinator logic, Gate policy, Claude invocation,
API key, or generic cross-engine workflow framework.
