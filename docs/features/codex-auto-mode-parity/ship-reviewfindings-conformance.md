**Status**: draft
**Owner**: @codex:michael
**Date**: 2026-07-29
**Related**: Ship dossier task `reviewfindings-shared-conformance-corpus` (`tsk_01KYPYX4EQTVC8S3Z1HAQXSZXK`)

# Ship ReviewFindings conformance

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Fixtures/tests | Ship driver/store ReviewFindings suites | ~420 | 210 |
| **Total** | | | **~210** |

Band: **amazing**.

## Goal

Prove Ship and Workbench's independent lifecycle consumers produce the same
address accept/refuse projection without introducing a shared runtime call
stack.

## Behavior / fix

- Canonical fixtures live at
  `workbench/contracts/reviewfindings/testdata/address-v1/` with a manifest and
  content digest.
- Each scenario includes artifact bytes, live head/cycle/consumed-id setup,
  ordered accept/resume calls, expected refusal code, and a common
  accept/consumption/at-most-once projection. Consumer-specific expectations
  are separate: Workbench address-work/claim state and Ship provider-call
  count.
- Vendor the exact corpus under
  `ship/packages/driver/testdata/reviewfindings-address-v1/`, preserving the
  source manifest/digest.
- Drive Ship's existing parser, engine, and store over every fixture.
- Cover supported/unknown major, exact head, empty/unsourced findings,
  source/panel consistency, remaining cycle capacity, duplicate id/digest, and
  bounded repeated consumption.
- Require paired reviewed updates when the canonical corpus digest changes;
  do not claim CI can fetch another repository implicitly.

## Acceptance

Ship produces every expected accept/refuse code, common consumption projection,
and Ship-specific provider-call count; vendored bytes match the recorded
upstream digest; bounded duplicate sequences satisfy the common at-most-once
law; drift is visible as a source-digest change.

## Test plan

Focused driver/store tests, full `make check`, and Ubuntu/Windows CI.

## Non-goals

No Workbench binary dependency in Ship, cross-repo import, provider dispatch,
cloud runtime, or coordinator/Gate policy change.
