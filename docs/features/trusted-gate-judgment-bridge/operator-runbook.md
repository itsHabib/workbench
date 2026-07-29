# Gate executor operator runbook

Status: prepared only. None of these actions were performed by Codex.

**Do not activate this runbook.** The executor workflow is source-disabled.
Do not register the App, create secrets or an environment, apply rulesets,
initialize state, mint a grant, remove the workflow guard, bootstrap, or run a
canary.

The operator authorized the one-App execution ordering. Repository code now
keeps approval verification, App token creation, claim CAS/refetch, exact
merge, and result CAS inside one Gate action. Generic Actions remains read-only.
Expired claims use a separate reconciler action: claim ID in, result CAS out.
It accepts no request document or merge argv and its image contains no GitHub
CLI. Local validation is green; CI and fresh exact-head review remain.

## 1. Review the dormant artifacts

Confirm the implementation PR/head and inspect:

- `.github/workflows/gate-executor.yml`
- `.github/actions/gate-executor/action.yml`
- `.github/actions/gate-reconciler/action.yml`
- `docs/features/trusted-gate-judgment-bridge/ruleset-plan-v1.json`
- the four schemas under `contracts/gateauthorization/schema/`

Do not register or configure anything if the reviewed head differs.

The protected `gate-authorization` environment must accept deployments only
from the selected protected `main` branch, with administrator protection-rule
bypass disabled. This platform rule is load-bearing: `workflow_dispatch`
accepts another ref, so a workflow guard alone cannot protect the App key.

## 2. Reconciliation trust boundary

GitHub requires `contents: write` both to update `gate-state` and to merge a
pull request. An installation token may narrow an App's permissions and
repository set, but it cannot narrow `contents: write` to one branch.

The operator chose one-App code-path separation. Dispatch operation
`reconcile` with exactly one expired `gxc_` claim ID. The reconciler re-fetches
and audits that claim, observes the PR, and writes one terminal result through
the same non-force state CAS. It has no merge input or merge call.

This does **not** create a cryptographically state-only credential. Its
installation token still has `contents: write` and is technically
merge-capable. The safety claim is deliberately narrower: protected release,
one process, a claim-only API, no GitHub CLI in the image, no merge operation,
and branch rules that remain operator-owned.

After operator activation only, the recovery dispatch shape is:

```powershell
gh workflow run gate-executor.yml -R itsHabib/workbench --ref main `
  -f operation=reconcile -f claim=gxc_<64-lowercase-hex>
```

The workflow remains hard-disabled today, so this command cannot release the
App credential or change state.

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
