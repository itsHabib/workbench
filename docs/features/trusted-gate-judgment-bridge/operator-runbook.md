# Gate executor operator runbook

Status: prepared only. None of these actions were performed by Codex.

**Do not activate this runbook.** The executor workflow is source-disabled.
Do not register the App, create secrets or an environment, apply rulesets,
initialize state, mint a grant, remove the workflow guard, bootstrap, or run a
canary.

The operator authorized the one-App execution ordering. Repository code now
keeps approval verification, App token creation, claim CAS/refetch, exact
merge, and result CAS inside one Gate action. Generic Actions remains read-only.
The implementation still requires local/CI validation and fresh exact-head
adversarial review.

## 1. Review the dormant artifacts

Confirm the implementation PR/head and inspect:

- `.github/workflows/gate-executor.yml`
- `.github/actions/gate-executor/action.yml`
- `docs/features/trusted-gate-judgment-bridge/ruleset-plan-v1.json`
- the four schemas under `contracts/gateauthorization/schema/`

Do not register or configure anything if the reviewed head differs.

The protected `gate-authorization` environment must accept deployments only
from the selected protected `main` branch, with administrator protection-rule
bypass disabled. This platform rule is load-bearing: `workflow_dispatch`
accepts another ref, so a workflow guard alone cannot protect the App key.

## 2. Reconciliation permission decision required

GitHub requires `contents: write` both to update `gate-state` and to merge a
pull request. An installation token may narrow an App's permissions and
repository set, but it cannot narrow `contents: write` to one branch.

The operator must choose one honest recovery model:

1. **One App, code-path separation.** A separate reconciler accepts only an
   expired claim ID, never accepts merge argv, and never invokes merge. Its
   token remains technically merge-capable because it needs `contents: write`
   to append the result.
2. **Two Apps, credential separation.** A state-writer App has bypass only on
   `gate-state`; the executor App has PR-only bypass only on `main`. The
   reconciler then receives a credential that cannot update `main`.
3. **Equivalent external monotonic writer/pin.** Supply an independently
   reviewed mechanism with the same rollback and branch-authority properties.

The first choice preserves the authorized one-App model but weakens the literal
"no-merge-token" claim to "no merge operation." The second is stronger but is
not a one-App model.

## 3. Activation remains operator-only

After that decision is implemented, locally green, CI-green, and freshly
reviewed, replace this hold with exact instructions for:

- App registration and minimum permissions;
- protected-environment reviewers and secrets;
- selected protected `main` as the environment's only deployment branch, with
  administrator protection-rule bypass disabled;
- `gate-state` initialization;
- staged five-layer ruleset application with no unprotected interval,
  including the independent no-bypass `gate-state-integrity` layer;
- positive and negative canaries;
- bootstrap, recovery, and rollback.

Until then every operational step is **stop**.
