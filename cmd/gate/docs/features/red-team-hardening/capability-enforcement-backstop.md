**Status**: draft
**Owner**: @michael
**Date**: 2026-07-06
**Related**: dossier task `capability-enforcement-backstop` (id: `tsk_01KWW33V10AARGVDN17V7NGXY0`), [docs/FOLLOWUPS.md](../../FOLLOWUPS.md)
**Model/effort**: opus / max

# Write down and enforce the capability backstop — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Docs | `docs/enforcement.md` (new), `README.md` (trim overclaims) | ~140 | 40 |
| Production source | `cmd/gate/main.go` (`cmdBacktest` mint) | ~15 | 15 |
| Tests | backtest/mint assertion as needed | ~30 | 15 |
| **Total** | | | **~70** |

Band: **amazing** per repo's PR sizing convention.

## Goal

The capability plane is voluntary today, so recon #5's indictment — "an agent that can merge can merge anything, forever" — is still true after gate ships. Minting is unprivileged (anyone who can run `gate` can run `gate grant`), `cmdBacktest` self-mints a spendable T2 grant, and every agent gate governs already holds a `gh` token that can `gh pr merge` straight around the gate. No document states the enforcement backstop. Write it down honestly, stop `backtest` from minting a spendable grant, and stop the README implying the gate bounds anything it cannot actually force.

## Behavior / fix

### 1. Write the enforcement model — new `docs/enforcement.md`

A dedicated doc (kept separate from `docs/DESIGN.md`, whose threat-model section the sibling tamper task owns — this avoids a docs conflict). It must state, plainly and without overclaiming:

- **What actually forces merges through the gate: GitHub branch protection.** The gate bounds nothing unless the target repo requires the `gate` status check on its protected branch (and disallows admin bypass / direct pushes). Until that is configured, the capability plane is *discipline plus an audit trail*, not prevention. Name this explicitly.
- **Token custody.** The merge-capable identity must not be the identity the gate is meant to bound. Describe the intended model: the gate (or a CI identity wired to it) holds the only token that can land a merge on the protected branch; the agents the gate governs hold tokens that can push branches and open PRs but cannot merge. State clearly that on the current single-box setup this separation is not yet real — every local agent shares one `gh` credential — and that closing it is a precondition for `-live`.
- **Mint authority and where the mint key lives.** Who/what may mint a grant, and where `grant.key` lives. Cross-reference the tamper task's decision to move `grant.key` out of the state dir (do not re-specify the path here; point at it). `MintedBy` is currently a free string with no authentication — say so.
- **The bypass, named.** An agent with `gh pr merge` rights bypasses the gate entirely. The doc must name this bypass rather than imply it's closed.
- **Operator action (not code).** A written, named step: *enable branch protection on the target repo(s) requiring the `gate` check before any `-live` wiring.* This is a repo-settings action the operator performs; the doc records it as the gating precondition.

### 2. Remove `cmdBacktest`'s spendable self-mint (`cmd/gate/main.go`)

`cmdBacktest` calls `capability.Mint(...T2, "backtest", time.Hour...)`, writing a real, spendable T2 grant into the state log as a side effect of a read-only backtest. Give backtest a clearly test-only / ephemeral capability path so it can still run its dry-run gate passes without leaving a spendable grant in state. Options the agent may choose between (pick the smallest honest one):
- run backtest's dry-run passes without minting a spendable grant at all (backtest is `-live`-free — it only ever produces `would_merge`/`blocked`/`parked`, so it may not need a real grant), or
- mint into an ephemeral/throwaway location that never joins the durable state log, clearly labelled test-only.

Whichever path: after `gate backtest ...`, **no spendable grant appears in the durable state log**.

### 3. Trim the README (`README.md`)

Wherever the README implies the gate *bounds* or *prevents* merges, soften to what is actually true today (bounds the gate's own sanctioned verbs; provides an audit trail; becomes enforcing only under branch protection). Link to `docs/enforcement.md` for the full model. Do not overclaim non-repudiation or prevention.

## Acceptance

- `docs/enforcement.md` exists and states the enforcement model: branch protection as the forcing function, token custody (and that it isn't real on the single box yet), mint authority + key location (cross-referencing the tamper task), the named `gh pr merge` bypass, and the operator branch-protection action as the `-live` precondition.
- `gate backtest -repo R -prs ...` produces **no spendable grant** in the durable state log.
- `README.md` no longer implies the gate forces/bounds merges it cannot actually force; it points at `docs/enforcement.md`.
- `go test ./...` green after the backtest change.

## Test plan

- Doc review (enforcement model reads honestly; no overclaim).
- `go test ./...` green.
- Manual/asserted: run `gate backtest` against a scratch `-state` dir, then confirm no `grant` artifact with a spendable ceiling was appended (assert on the state log, or a unit test around the refactored backtest path).

## Non-goals

- The hash-chain / tamper hardening and the `grant.key` *path change itself* — that's the sibling tamper task (this doc only references the key-custody decision).
- Actually configuring branch protection in GitHub — that's the operator repo-settings action, named in the doc, not performed in this PR.
- Driver wiring; adding real mint authentication (documented as future, not built here).
- The verifier-ladder fail-opens — sibling task.
