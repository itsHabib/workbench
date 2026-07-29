# Gate executor operator runbook

Status: prepared only. None of these actions were performed by Codex.

**Do not activate this runbook.** The executor workflow is hard-disabled and
the symbolic plan leaves `gate-state` writerless while the operator decides
the state-writer custody/order amendment described in `design.md`. Do not
register the App, create secrets/environment, apply rulesets, initialize
state, mint a grant, remove the workflow guard, bootstrap, or run a canary.

This runbook begins after the repository implementation PR is locally green,
CI-green, and exact-head adversarial review is clean. It intentionally separates
operator authority from repository implementation.

## 1. Review the dormant artifacts

Confirm the implementation PR/head and inspect:

- `.github/workflows/gate-executor.yml`
- `.github/actions/gate-executor/action.yml`
- `docs/features/trusted-gate-judgment-bridge/ruleset-plan-v1.json`
- the four schemas under `contracts/gateauthorization/schema/`

Do not register or configure anything if the reviewed head differs.

## 2. Security decision required before operational instructions

There is no valid registration, environment, state initialization, ruleset,
bootstrap, canary, or rollback procedure for this held implementation. The
previous procedure selected GitHub Actions as the `gate-state` writer; that is
the rejected custody model and must not be reconstructed.

The operator must first approve either:

1. the one-App process-custodied ordering proposed in `design.md`, including
   durable claim/result CAS and no-merge-token expired-claim reconciliation; or
2. an equivalent design with an independent state writer or external monotonic
   pin that proves rollback resistance.

After that decision is implemented and receives a fresh exact-head security
review, replace this hold with a new runbook that names the exact App
permissions, environment policy, state writer, branch identities, bootstrap,
positive/negative canary, recovery, and rollback. Until then, every operational
step is “stop.”
