# Trusted Gate judgment bridge

Status: authorized one-App ordering implemented in dormant repository code;
reconciliation permission decision, review, and operator bootstrap remain.

**Security hold:** the repository implementation is intentionally non-armable.
Fresh review proved that the coarse `github_actions` Integration cannot be an
exclusive `gate-state` writer. The operator therefore authorized one
process-custodied Gate App to publish claim/result CAS commits and execute the
exact merge. The workflow still has a hard false guard. One platform limitation
remains explicit below: GitHub uses `contents: write` for both ref updates and
PR merge, so the same App cannot mint a literally state-only reconciliation
token.

The operator authorized this trust model at design head
`745d2bc405e07fd202c2379320afdc1745e46cc5`. That authorization covers repository
code, tests, and documentation only. App registration, secrets, environment
configuration, ruleset changes, grant minting, state bootstrap, live execution,
and merge remain operator-only.

The operator separately authorized the one-App process-custodied ordering at
held PR head `19dae14d5cc71d3859938ffe218230d542f7498f`: post-approval
token creation, claim CAS/refetch, exact merge, result CAS, and an intended
no-merge-token expired-claim reconciliation path.

## Decision

Gate uses a PR-specific custodied executor, not commit-status promotion.
Provider-neutral judgment may authorize exactly one Gate action for exactly one
pull request. It does not create reusable green status.

The trust root is the conjunction of:

1. one run-specific approval from a protected `gate-authorization` GitHub
   Environment, made by an immutable actor ID different from the workflow
   dispatcher/re-run actor; and
2. one repository-only Gate GitHub App whose private key is released only to
   the custodied executor job after protected approval. Gate creates its token
   only after all pre-credential refusals and before the permanent claim,
   because that same App must publish the claim.

Neither side is sufficient alone. An unsigned request, login allowlist, ambient
operator token, inherited commit status, App token without a claim, or claim
without a protected approval authorizes nothing.

## Versioned artifacts

Shared vocabulary lives in `contracts/gateauthorization`:

- `GateAuthorizationRequestV1` — exact repository, PR, head, protected base
  ref, evaluated base SHA, merge-base SHA, Gate run/action ID and hash,
  `would_merge`, exact argv, canonical evidence digest, judgment question,
  issue/expiry bounds, replay ID, and trust-root name.
- `GateAuthorizationV1` — the request plus the verified workflow-run approval
  receipt. The receipt records immutable actor login/ID, approval state,
  protected-job observation time, exact comment, run ID, and semantic digest.
  GitHub does not expose an approval submission timestamp.
- `GateExecutionClaimV1` — the complete authorization plus action, exact argv,
  exact-subject grant ID, approval digest, claim/expiry times, and stable
  one-time execution identity.
- `GateExecutionResultV1` — one terminal `merged` or `failed` record with no
  credential or command output.

Schemas:

- `contracts/gateauthorization/schema/gate-authorization-request-v1.json`
- `contracts/gateauthorization/schema/gate-authorization-v1.json`
- `contracts/gateauthorization/schema/gate-execution-claim-v1.json`
- `contracts/gateauthorization/schema/gate-execution-result-v1.json`

Authorization, claim, and execution IDs are hashes of canonical semantic
content. Retrying a delivery cannot mint a new identity.

`contracts.Verdict` and `ReviewPanelV1` remain the clean/pass, escalation, and
block evidence. `ReviewFindingsV1` remains address-work-only; a clean review
never fabricates an empty findings artifact.

## Flow

1. `gate executor request` audits an existing newest `would_merge` action,
   byte-checks its stored argv, re-reads the live PR, and emits the request plus
   exact approval comment.
2. The operator dispatches `.github/workflows/gate-executor.yml` from `main`
   with the request. The static protected environment accepts deployments only
   from the selected protected `main` branch, with administrator bypass
   disabled. The environment holds the job; its approver supplies the exact
   emitted comment.
3. After approval, Gate reads the workflow-run and approval-history APIs. It
   binds the first run attempt to this repository, `workflow_dispatch`, the
   exact `main` workflow path, and the current `main` commit; takes both initial
   and triggering actor identities from their immutable API IDs; and requires
   exactly one unambiguous decision for `gate-authorization` to be `approved`
   by a different actor, with the exact canonical comment. Re-runs refuse
   because GitHub's approval history is not attempt-bound.
4. Gate re-reads exact repo/PR/head/base/merge-base facts, re-audits anchored
   state, checks action freshness and duplicate/open claims, and confirms the
   checked-out `gate-state` tip still equals the remote tip. These are the
   pre-credential refusals.
5. Only then does the Gate process exchange the protected App key for one
   short-lived installation token narrowed to this repository with
   `contents: write`. The token is neither returned nor exposed to another
   workflow step.
6. Inside that process Gate mints the exact-subject HMAC grant and appends one
   permanent claim. The App creates blobs/tree/commit through GitHub's Git Data
   API, advances `gate-state` without force from the exact expected parent, and
   reads the remote claim bytes back.
7. Gate audits the refetched claim, re-reads the PR/head/base/merge-base, checks
   the remote state tip again, and runs only the stored ten-element
   `gh pr merge ... --match-head-commit` argv.
8. Gate records one terminal result and advances `gate-state` through the same
   non-force CAS. A transport failure after claim leaves an orphaned claim; it
   never retries the merge.

The workflow builds the exact dispatched `main` commit, never checks out
PR/fork code, has no status-write permission, and gives generic Actions
read-only repository permissions.

## State and branch identities

`GATE_ANCHOR_RECORD` allows the keyed anchor record to live beside the hosted
state tree while both signing keys remain outside it. A fresh runner must have:

- `gate-state/state/log.jsonl`
- `gate-state/anchor.json`
- protected `grant.key` and `anchor.key` restored outside that tree

The workflow's built-in token is read-only. The Gate App is the only planned
writer of `gate-state`, and its token is created only inside the executor
process after approval and preflight.

The staged, non-effectful policy is
`docs/features/trusted-gate-judgment-bridge/ruleset-plan-v1.json`. A focused
test pins its complete bytes. The plan has five layers:

- `main-updates`: Gate App is the sole Integration with PR-only bypass;
- `gate-state-updates`: only the Gate App may update the ref;
- `gate-state-integrity`: no actor, including the Gate App, may force-push or
  delete the ref;
- `other-branches`: repository roles and approved existing integrations, never
  the Gate App; and
- `main-required-gate`: retain the app-pinned `gate` check, with only the same
  Gate App receiving PR-only bypass.

Consequences:

- a second PR sharing the head cannot inherit execution authority;
- ambient users and Actions cannot update `main`, even if a status is green;
- the Gate App cannot merge a retargeted PR to a non-`main` branch; and
- the Gate App can close the existing red-check deadlock only for its exact
  claimed PR.

## Refusal semantics

All of these refuse before App token creation:

- malformed/unknown major, noncanonical repo, bad IDs/SHA/times, unsupported
  outcome, changed method/flags/order, or `--admin`;
- missing, stale, rejected, self-authored, wrong-run, re-run, ambiguous,
  wrong-environment, wrong-comment, non-`main` dispatch, stale workflow commit,
  or noncanonical workflow path approval;
- expired/not-yet-valid request;
- closed/moved/shared-head PR, non-default target, base retarget, base movement,
  or merge-base mismatch;
- a merge target outside `main`; the staged `~ALL` other-branch rule restricts
  updates to explicit normal writers and gives the Gate App no bypass;
- action absent, hash/run/argv mismatch, older action superseded by any newer
  action/park, duplicate claim, open claim, or durable-state audit failure; and
- checked-out and remote `gate-state` tip mismatch.

After token creation, any grant/claim failure, claim CAS conflict, malformed
refetch, remote audit failure, changed live PR/base facts, merge-command
failure, or result CAS conflict fails closed. Once the claim is durable, the
merge is never retried.

## Open security decision: expired-claim reconciliation

GitHub's permission model does not distinguish these two operations:

- updating `gate-state` requires repository `contents: write`; and
- merging a pull request also requires repository `contents: write`.

An installation token can request fewer permissions than its App, but it
cannot request branch-scoped permissions. Under the one-App ruleset, any token
capable of closing an orphaned claim on `gate-state` is therefore also
credential-capable of updating `main`. A reconciler binary that contains no
merge call is useful process separation, but it is not literally a
no-merge-capability token.

The remaining honest choices are:

1. accept one App and define "no-merge-token reconciliation" as a separate,
   default-branch-only Gate code path that never accepts argv or invokes merge,
   while acknowledging that its `contents: write` token is
   credential-capable;
2. use a second state-writer App whose ruleset bypass applies only to
   `gate-state`, giving reconciliation a credential that cannot update `main`;
   or
3. choose an equivalent external monotonic writer/pin.

Repository activation remains stopped until the operator selects that trust
semantics. No option changes the one-shot rule: token exchange, command,
confirmation, or terminal-state write failure never creates reusable success.

## Dormant bootstrap boundary

Repository presence is intentionally insufficient to run this path. The
workflow references App/environment/key values that do not exist until the
operator follows `operator-runbook.md`. The implementation does not register an
App, create or populate secrets, configure the environment, apply rulesets,
initialize `gate-state`, mint a grant, bootstrap itself, or merge.

The required live canary begins with `gate` red and must prove:

- exact stored argv succeeds as the Gate App without `--admin`;
- ambient-user and Actions updates to `main` refuse;
- Gate App update to a non-`main` branch refuses;
- shared-head, retarget, stale, malformed, duplicate, and replay cases create no
  App token or merge; and
- claim/result, GitHub actor, argv, PR head/base, and merge commit agree.

Until that proof passes, this is reviewed dormant code, not an armed security
boundary.
