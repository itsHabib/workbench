**Status**: draft
**Owner**: @codex:michael
**Date**: 2026-07-29
**Related**: dossier task `codexguard-fresh-task-dogfood` (`tsk_01KYP78SSC77DRNABJB8F3VAVF`)

# Fresh-task auto-mode dogfood

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Test/evidence | `cmd/codexguard/testdata/e2e/`, phase evidence docs | ~360 | 180 |
| **Total** | | | **~180** |

Band: **amazing**.

## Goal

Prove from a fresh Codex task that the installed policy blocks unauthorized
authority shapes, allows the exact Gate-emitted merge shape, records replayable
evidence, and completes the native exact-head review artifact seam.

## Behavior / fix

- Install through the reviewed projection path, first into a temporary profile.
- Run Bash, PowerShell, local-function, and MCP refusal/bypass fixtures.
- Prove a bare merge is blocked with the exact Gate remedy.
- Prove a full-SHA Gate-shaped merge is allowed by policy. Separately capture
  Gate stdout and byte-compare the tested argv before claiming that sample was
  Gate-emitted; do not execute it without authorization.
- Use the first useful phase PR as `/review-coordinator` input, emit
  `ReviewFindingsV1`, consume it through the session-native address boundary,
  delegate the resulting work item to a fresh Codex child on the existing PR
  branch, and exercise stale, malformed, duplicate, exhausted, and unsourced
  refusal paths before dispatch.
- Record every intervention and secret-safe artifact reference.
- Use the existing GitHub connector or `gh`/git authentication for PR reads and
  pushes; the forbidden credential is a model-provider SDK/API key.

## Acceptance

A fresh task discovers/trusts the policy; required refusals occur before
execution where Codex has a valid hook/rules decision, with every unsupported
hook-failure row recorded honestly; the native exact-head artifact is accepted
exactly once; the final evidence is replayable and contains no secrets.

## Test plan

Fresh-task dogfood, checked-in replay fixtures, full Workbench checks, exact-head
review, and Gate only with an operator-minted grant.

## Non-goals

No Ship cloud/provider run, Cursor or other model-provider API key, Claude
invocation, grant minting, `--admin`, or weakening checks.
