**Status**: draft
**Owner**: @michael
**Date**: 2026-07-13
**Related**: dossier task `enforce-gate-on-canary-repo` (id: `tsk_01KXDYG0YQEKRDMPE4T4YX1RF5`, workbench project, talk-readiness phase), [docs/enforcement.md](../../enforcement.md)

# Enforce gate on the canary repo: status check + branch protection — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `.github/workflows/gate.yml` (new) | ~90 | 90 |
| Docs | `docs/enforcement.md`, `README.md` | ~90 | 90 |
| **Total** | | | **~180** |

Band: **amazing** per repo's PR sizing convention — small diff, trust-boundary critical.

**Depends on**: `wire-gate-as-driver-merge-step` (driver-wiring phase; `in_progress` as of 2026-07-13 — this consumes its exit-code contract and MUST NOT start until it lands).

## Goal

Per `docs/enforcement.md`, gate today is discipline plus an audit trail: nothing forces a merge through it. A merge-capable `gh` token bypasses it silently. The enforcement claim upgrades from advisory to enforced-on-one-repo only when itsHabib/gate (the designated canary, per driver-merge-wiring P6) has branch protection requiring a `gate` status check — spec §10 posture 2, explicitly out of scope of the `-live` task and unowned until now.

## Behavior / fix

- A GitHub Actions workflow on itsHabib/gate that runs the gate against the PR and publishes a `gate` check: builds `./cmd/gate`, gathers evidence for the PR, runs the ladder, maps the exit-code contract (from `wire-gate-as-driver-merge-step`) to check conclusion. Escalate/park land as a *failing* check (fail closed) with the outcome JSON in the check summary.
- CI-context state handling: ephemeral state dir per run (the backtest `newEphemeralEnv` precedent) OR a persisted artifact trail — decide in-PR and document which and why; do not silently fork the state model.
- Operator actions, written as a numbered runbook in `docs/enforcement.md`: enable branch protection requiring the `gate` check, disallow admin bypass, disallow direct pushes.
- Update `docs/enforcement.md` honestly: what this closes (direct-merge bypass on the canary) and what stays open (token custody — every local agent still shares one merge-capable credential; mint authentication).

This is a trust-boundary change: per house policy it gets the adversarial skeptics-break-the-gate Workflow pass (structural fail-opens: can the check be skipped/neutral? does a missing evidence fetch read as green? does a fork PR run the workflow with a token?) in addition to the bot panel.

## Acceptance

- A PR on itsHabib/gate cannot merge while the `gate` check is red or absent (verified by attempting one).
- A gate block/escalate → failing check with the outcome JSON visible; pass within grant → green check.
- Adversarial pass finds no structural fail-open in the workflow path (skip/neutral, missing-evidence, fork-PR token).
- `docs/enforcement.md` names what is now enforced and what is still open, and carries the operator runbook.

## Test plan

One canary PR each for: pass (merges), block (cannot merge), and check-absent (cannot merge). Workflow-level: evidence-fetch failure produces a failing check, not green. The adversarial Workflow report attached to the PR.

## Non-goals

`-live` merge execution (existing `adversarial-gate-before-live-merge` task); token custody / merge-only identity (documented as open, not built); mint authentication; extending enforcement beyond the one canary repo.
