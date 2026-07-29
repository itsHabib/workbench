# Gate executor operator runbook

Status: prepared only. None of these actions were performed by Codex.

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

## 2. Register the dedicated repository-only App

Operator action:

1. Register a GitHub App dedicated to Gate execution.
2. Give it repository contents write and metadata read only; no organization
   permissions, webhook, status/check write, administration, actions write,
   secrets, or members permission.
3. Install it only on `itsHabib/workbench`.
4. Record the immutable App ID and installation ID.
5. Generate one private key for the protected environment secret. Never place
   it in local agent-readable configuration, repository files, logs, or a
   general workflow environment.

Stop if GitHub requires a broader permission than the executor's exact merge
call. Review that as a new security decision.

## 3. Configure the protected environment

Create `gate-authorization` with:

- at least one required reviewer whose GitHub credential is unavailable to
  governed agent sessions;
- prevent self-review enabled;
- administrator bypass disabled; and
- deployment restricted to protected `main`.

Add only:

- secret `GATE_APP_PRIVATE_KEY`;
- secret `GATE_GRANT_KEY_B64`;
- secret `GATE_ANCHOR_KEY_B64`;
- variable `GATE_APP_ID`; and
- variable `GATE_APP_INSTALLATION_ID`.

Both HMAC secrets must decode to at least 32 random bytes. Empty, missing, or
short decoded keys are refused before approval consumption or state mutation.

The independent reviewer must compare the request fields and submit the exact
comment printed by `gate executor request`.

## 4. Initialize hosted state

From an operator-controlled checkout:

1. Create orphan branch `gate-state`.
2. Add `state/log.jsonl` from the canonical audited Gate state and
   `anchor.json`; do not add either key.
3. Verify `gate audit` succeeds on a fresh clone using restored keys and
   `GATE_ANCHOR_RECORD=<clone>/anchor.json`.
4. Record the bootstrap commit and canonical local ingestion direction.

No agent performs this copy because it changes the trust channel and may expose
operator state.

## 5. Apply the layered rules without an unprotected interval

Resolve the symbolic `gate_app` and `github_actions` actor references in
`ruleset-plan-v1.json` to GitHub's immutable Integration IDs. Stage and verify,
in an order that never leaves `main` less protected:

1. Create `main-required-gate`, retaining the existing `gate` context and
   current GitHub Actions App pin; add only the Gate App PR-only bypass.
2. Create `main-updates`, retaining PR, deletion, and non-fast-forward
   protection; Gate App is the sole PR-only update bypass.
3. Create `gate-state-updates`; GitHub Actions is the sole writer and force
   updates/deletion remain denied.
4. Create `other-branches`; Gate App is absent.
5. Query effective rules for `main`, `gate-state`, and one disposable other
   branch. Compare them with the plan before retiring overlapping legacy rules.

Never use a repository-role bypass for the operator or GitHub Actions on
`main`.

## 6. Bootstrap the implementation

The dormant executor cannot merge the PR that introduces itself. Choose and
perform the reviewed one-time bootstrap. Do not use `--admin` or temporarily
weaken checks. Record the exact PR/head, actor, command, and resulting main SHA.

If no no-weaker bootstrap exists, stop; do not improvise one.

## 7. Canary

Use a disposable PR whose `gate` check is intentionally red:

1. Produce a newest exact-head `would_merge` action.
2. Run `gate executor request` and inspect its subject, base, evidence digest,
   question, action hash, and exact argv.
3. Dispatch `gate-executor.yml` from `main`.
4. Approve from the independent identity with the exact comment.
5. Prove one App-authored PR merge, unchanged argv, no `--admin`, one claim,
   and one terminal result.
6. Prove ambient user main update, Actions main update, and Gate App non-main
   update all refuse.
7. Run stale head, shared head, retarget, base movement, malformed request,
   wrong comment, duplicate claim, and replay refusals.

Do not use PRs #165 or #168 until the disposable canary passes.

## 8. Rollback

If any identity or canary invariant fails:

1. Reject/cancel pending `gate-authorization` deployments.
2. Disable/uninstall the Gate App or remove its private-key secret.
3. Preserve `gate-state` and Actions logs as evidence.
4. Restore the previous equivalent protections through an operator-reviewed
   ruleset change; never open `main`.
5. File the exact discrepancy before another attempt.
