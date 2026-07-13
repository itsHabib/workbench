**Status**: accepted
**Owner**: @codex:control-room
**Date**: 2026-07-13
**Related**: dossier task `control-room-adapter-github` (id: `tsk_01KXDPT2HSGBT2MCD292F2DAQ2`), [`../spec.md`](../spec.md)

# Control Room bounded GitHub GraphQL adapter — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Production source | `cmd/controlroom/internal/adapters/github/*.go` | ~170 | ~170 |
| Scripted GraphQL tests | same package | ~180 | ~90 |
| **Total** | | **~350** | **~260** |

Band: **stretch** per the repository PR-sizing convention.

## Goal

Populate the authoritative PR inventory through one bounded, explicitly scoped GraphQL adapter without per-PR subprocess fan-out or silent completeness claims.

## Behavior / fix

- Add only `cmd/controlroom/internal/adapters/github`. Validate one to four unique scopes, each exactly `user:<login>`, `org:<login>`, or `repo:<owner/name>`; reject ambiguous/unscoped input before execution.
- The package may import but must not modify `cmd/controlroom/internal/model`. Export a source-local `Result` containing `[]model.PullRequest` plus `model.SourceReceipt`; if a required shared model type is absent, stop and escalate rather than extending the locked package.
- Require `gh >= 2.90.0`, resolve the authenticated login with `gh api user --jq .login`, and invoke the accepted embedded/versioned query only through `gh api graphql`.
- Query `is:pr is:open author:<authenticated-login> archived:false` plus each explicit scope. Page scopes in stable round-robin order, at most four total pages of 50 PRs under the source's single 10-second collection context deadline (not ten seconds per page), and deduplicate by node ID.
- Normalize repository, number, title/URL, author, branches/head OID, draft/state/times, checks, review decision/requests, review threads, mergeability, and merge-state facts into the locked model. Missing known fields are unknown; additive fields are ignored.
- If an unvisited inventory page remains after the cap, return current partial data with `degraded/inventory_truncated`. Retain completed pages when a later page fails. Saturated check/thread connections set `detail_state=truncated`; observed negative evidence remains available but positive readiness facts stay suppressed by the existing policy.
- Use injected command seams and sanitized typed failures. Never execute `gh pr view` or one subprocess per PR.

## Acceptance

Scope/version/auth validation, additive/missing fields, empty checks, round-robin paging, 200-PR cap, later-page timeout/failure, duplicate nodes, saturated nested connections, and receipt/error invariants match the accepted TDD. Ordering and deduplication are deterministic.

## Test plan

Use a scripted fake `gh` executable. Assert exact argv, GraphQL variables/query version, scope isolation, page order/cap, retained partial results, truncation semantics, no per-PR fan-out, stable normalization, and sanitization. Run the full Go gates.

## Non-goals

Live PR detail collection, cross-source joins, UI work, hidden per-PR queries, GitHub mutations, or shared adapter helpers outside this source directory.
