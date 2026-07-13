**Status**: draft
**Owner**: @codex:control-room
**Date**: 2026-07-13
**Related**: dossier task `control-room-adapter-tower` (id: `tsk_01KXDPT2KC0M17G0J4JCH9F398`), [`../spec.md`](../spec.md)

# Control Room optional Tower adapter — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Production source | `cmd/controlroom/internal/adapters/tower/*.go` | ~60 | ~60 |
| Tests and fixtures | same package | ~70 | ~35 |
| **Total** | | **~130** | **~95** |

Band: **amazing** per the repository PR-sizing convention.

## Goal

Expose optional supplemental worktree/branch context while making Tower's absence normal and preventing reconciliation, GitHub-authority drift, or local-path leakage.

## Behavior / fix

- Add only `cmd/controlroom/internal/adapters/tower`. When configured, execute exactly `tower ls --json --no-reconcile` with a bounded context; executable absence returns a normal unavailable Tower receipt and no rows.
- Normalize only stable repository/worktree/branch identity and supplemental path facts that fit the locked model. Tower never owns PR/check/review freshness.
- Ignore additive fields. Malformed output, timeout, nonzero exit, duplicate/ambiguous repository identity, and unsafe paths fail or qualify only Tower.
- Preserve paths as internal evidence for Phase 6 containment work; do not emit arbitrary absolute paths or local deep links to the browser. No mutation flag or reconcile-capable path may be configurable.

## Acceptance

Healthy, empty, additive, malformed, timeout, nonzero-exit, missing-executable, duplicate identity, and unsafe-path fixtures produce deterministic supplemental rows or an isolated unavailable receipt. Exact argv always includes `--no-reconcile`.

## Test plan

Use a fake executable. Assert argv, cancellation, stable identity/order, optional-source semantics, path redaction/qualification, sanitization, and absence of mutation flags. Run the full Go gates.

## Non-goals

Tower installation, reconciliation, GitHub authority, cross-source joins, deep-link enablement, or shared adapter helpers outside this source directory.
