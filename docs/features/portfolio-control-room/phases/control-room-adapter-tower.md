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

- Add only `cmd/controlroom/internal/adapters/tower`. The package may import but must not modify `cmd/controlroom/internal/model`; if a required shared type is absent, stop and escalate. Export a source-local `Result` containing adapter-local `[]Worktree` records plus `model.SourceReceipt`, so Phase 5 can compose supplemental facts without adding a Tower type to the shared model. When configured, execute exactly `tower ls --json --no-reconcile` with a bounded context; executable absence returns a normal unavailable Tower receipt and no rows.
- Normalize only stable repository/worktree/branch identity. Retain Tower path values as opaque adapter-local strings, never turn them into `model.SafeLink`, never access the filesystem, and never emit them directly to the browser. Phase 6 owns `filepath.Abs/Clean/EvalSymlinks/Rel` workspace containment before any path becomes actionable. Tower never owns PR/check/review freshness.
- Ignore additive fields. Malformed output, timeout, nonzero exit, and duplicate/ambiguous repository identity fail or qualify only Tower. Opaque paths do not fail Phase 4 collection solely because they are absolute or outside the workspace.
- No mutation flag or reconcile-capable path may be configurable.

## Acceptance

Healthy, empty, additive, malformed, timeout, nonzero-exit, missing-executable, duplicate identity, and arbitrary opaque-path fixtures produce deterministic supplemental rows or an isolated unavailable receipt. Exact argv always includes `--no-reconcile`, and no path is promoted to a safe/actionable link.

## Test plan

Use a fake executable. Assert argv, cancellation, stable identity/order, optional-source semantics, opaque-path pass-through without filesystem access or `SafeLink` promotion, sanitization, and absence of mutation flags. Run the full Go gates.

## Non-goals

Tower installation, reconciliation, GitHub authority, cross-source joins, deep-link enablement, or shared adapter helpers outside this source directory.
