# Goal kickoff — trusted Gate judgment bridge, Codex auto-mode, and live dogfood

**Status:** revised executor model authorized; repository implementation in progress
**Owner:** operator + Codex session seat
**Date:** 2026-07-29
**Primary repository:** `itsHabib/workbench`
**Related repositories:** `itsHabib/ship`
**Existing auto-mode umbrella:** `tsk_01KYMZF6CGMQXMWPHRB54AXDJQ`

Use this document as the complete goal input. Continue until the definition of
done is met or an operator-only authority decision is required. Do not replace a
real security boundary with prose, an ambient-token convention, or a thin
wrapper that owns no policy.

## Objective

Restore the governed merge path without invoking Claude or using a
model-provider cloud run, then execute the already-reviewed Codex auto-mode
program end to end with `/work-driver --engine session`.

The immediate defect is structural:

- Workbench branch protection requires the app-pinned `gate` status produced by
  `github-actions[bot]`.
- A provider-neutral local Gate judgment posts `gate/authorized` using the
  operator's ambient GitHub identity.
- `gate/authorized` is intentionally a provenance marker, not enforcement
  authority; requiring it directly would weaken the app-pinned boundary.
- The hosted `gate` workflow cannot currently consume a resolved
  provider-neutral judgment. When the configured Claude/Cursor panel is omitted,
  it remains red even after a valid local T3 judgment.
- Therefore GitHub refuses Gate's exact `--match-head-commit` merge command.

Build the smallest trusted bridge that lets a provider-neutral, exact-head Gate
authorization control one exact PR merge without making a user-posted status
authoritative.

## Security-review amendment — 2026-07-29

The first implementation attempt promoted `GateAuthorizationV1` through a
protected GitHub Environment into the existing app-pinned `gate` commit status.
It reached green CI at Workbench PR #169, head
`16179ef0c31c2de548fb7657e0bd47519313d690`, then failed the required fresh
adversarial review on three P1 authority defects:

1. Reading the environment's current configuration does not prove that the
   particular workflow run received an independent approval. A consumer must
   verify the run's approval history and approving actor.
2. A local `would_merge` exported before a newer block/park remains promotable
   unless consumption has a trusted freshness or revocation channel.
3. GitHub commit statuses are attached to a SHA, not a PR. Gate authorizes a PR
   number and exact head, so another PR backed by the same SHA can inherit
   `gate=success`. A read-before-write uniqueness check cannot prevent a PR
   created after the read or after success.

The third defect disproves the original assumption that the existing required
status can carry PR-specific merge authority by itself. Do not merge or arm the
status-promotion implementation, even after fixing the first two defects.

The replacement decision must choose a genuinely PR-specific enforcement seam:

- **Recommended:** extend the dedicated Gate GitHub App track so a
  protected, run-approved trusted-base workflow re-evaluates Gate at
  consumption time and the App executes only Gate's stored exact
  `gh pr merge ... --match-head-commit ...` argv for the artifact's PR. The App
  is the narrowly custodied Gate executor, not a prose wrapper. It must not
  publish reusable merge authority as a commit status.
- **Alternative:** move the repository to an organization and require GitHub's
  merge queue, then authorize the PR-specific temporary `merge_group` SHA.
  Merge queues are not available to this personal public repository.
- **Fallback with recurring human friction:** require an independent,
  PR-specific approval in branch protection in addition to the commit status.
  This gives every second PR a fresh human boundary but does not preserve the
  intended autonomous delivery loop.

The recommended App path is a material trust-model and branch-rule decision.
The operator authorized repository implementation of that model at
`745d2bc405e07fd202c2379320afdc1745e46cc5`. PR #169 remains draft while the
rejected status-promotion prototype is replaced. App registration, secrets,
environment configuration, ruleset changes, grant minting, bootstrap, live
execution, and merge remain stopped at the operator boundary.

## Ground truth to preserve

Read completely before changing anything:

1. `AGENTS.md`
2. `docs/DESIGN.md`
3. `docs/workbench-101.md`
4. `docs/auto-mode-defaults.md`
5. `cmd/gate/CLAUDE.md`
6. `cmd/gate/docs/DESIGN.md`
7. `cmd/gate/docs/enforcement.md`
8. `docs/features/gate-authorized-stamp/kickoff.md`
9. `docs/features/agentic-workbench-closure/spec.md`
10. the scoped `CLAUDE.md`/`AGENTS.md` for every additional tool touched

The historical `gate-enforcement-arming` kickoff is not present on current
`main`; `cmd/gate/docs/enforcement.md` is the authoritative shipped record.

Also inspect, but do not duplicate, the unfinished App-mint design:

- open PR `itsHabib/workbench#143`
- branch `docs/gate-app-mint-tdd`
- `docs/features/gate-app-mint/spec.md`

That TDD addresses authenticated, head-bound **grant minting** and later
credential authority. Its original non-goal excluded execution and treated the
commit status as sufficient enforcement. The amendment above invalidates that
separation: any revised App design must be reviewed as the PR-specific
consumption and execution boundary, while continuing to reuse Gate's contracts
and exact command rather than absorbing Gate policy into workflow prose.

Already shipped:

- PR #139: local `gate/authorized` provenance stamp.
- PR #149: hosted reviews are advisory to the readiness rung.
- PR #159: provider-neutral Gate judgment contract.
- PR #163: exact-head review-panel completeness.

Currently blocked but otherwise ready:

- PR #165: Gate-authorized exact head; required hosted `gate` remains red.
- PR #168: reviewed auto-mode preparation; CI green; independent exact-head
  Codex review clean; T3 provider-neutral Gate judgment passed; required hosted
  `gate` remains red.

Re-read live PR state before acting. Git and GitHub are truth.

## Non-negotiable boundaries

- The agent never mints a grant. The operator may provide one, or an
  authenticated App-mint workflow may mint one only after a verified,
  run-bound independent approval under the approved design.
- Never use `--admin`, dismiss findings, weaken required checks, or disable
  branch protection to make a merge pass.
- Never merge without Gate's exact emitted
  `gh pr merge ... --match-head-commit <full-sha>` command.
- Never reconstruct, loosen, or append flags to Gate's merge command.
- Never make the ambient user-posted `gate/authorized` status satisfy branch
  protection directly.
- Never invoke or depend on Claude.
- No Ship cloud run, Cursor SDK/API key, Anthropic/OpenAI provider key, or other
  model-provider cloud execution.
- Ordinary GitHub Actions CI and the protected Gate executor are in scope; they
  are repository enforcement, not agent/model cloud execution.
- Use `--engine session` for every implementation driver run.
- Use GitHub connector or existing `gh`/git authentication for repository I/O.
- Keep shared contracts versioned. Do not invent ad hoc JSON.
- Preserve the boundary law: tools share types/schemas and artifacts, never
  another tool's decision logic.
- Work in isolated `codex/*` worktrees from current `origin/main`.
- Keep public-repository material scrubbed: no grant IDs, credentials, private
  paths, private-repository content, operator-specific secrets, or local state.

## Program structure

```text
trusted judgment bridge
  -> operator-approved Gate App + equivalent-or-stronger ruleset bootstrap
  -> merge #165 and #168 through the PR-specific Gate executor
  -> Workbench five-stream session run
  -> Ship one-stream conformance run
  -> Workbench one-stream fresh-task dogfood
  -> close auto-mode umbrella
  -> physical/passkey custody hardening later
```

## Phase A — trusted Gate judgment bridge

Create or reconcile one Dossier task:

```text
project: workbench
phase: codex-auto-mode-parity
slug: trusted-gate-judgment-bridge
title: Bridge provider-neutral Gate judgments into a PR-specific Gate executor
```

Do not create a duplicate if an equivalent live task already exists.

PR #169 is the rejected status-promotion prototype and security record. Keep it
draft. After the operator approves the replacement trust model, supersede its
implementation with the smallest reviewable App-mint/executor sequence. Keep
the branch-rule migration and live credential bootstrap operator-only.

### A0. Audit and design decision

Audit the current Gate action artifact, provider-neutral judgment contract,
state/audit chain, stamp mechanism, hosted workflow, legacy branch protection,
repository ruleset, and GitHub App identities.

The replacement trust root is a conjunction, not either/or:

- a protected GitHub Environment whose **run-specific approval history** proves
  an independent approver authorized the canonical PR/head/evidence digest and
  judgment question; and
- a dedicated, repository-only Gate GitHub App whose private key is available
  only to the protected executor step and whose installation token never leaves
  the Gate process.

An opaque `repository_dispatch` payload, a user-authored commit status, a login
allowlist that agents can impersonate with the ambient operator token, or a
description containing an unverifiable hash is not sufficient.

If either the approval identity or App private key is available to the governed
agent identity, state that honestly and stop. Do not call convention-based
custody cryptographic enforcement.

Record the decision in a focused design section or ADR in the bridge PR.

### A1. Versioned authorization and execution artifacts

Use or implement a versioned shared contract, tentatively
`GateAuthorizationV1`, under a leaf `contracts` package. Final naming should
follow existing contract conventions.

The canonical signed/approved content must bind at least:

- schema/version
- repository
- PR number
- exact 40-character head SHA
- protected base ref, evaluated base SHA, and merge-base SHA
- Gate run ID
- deciding action artifact hash
- outcome (`would_merge` only)
- exact merge argv as an array, never reconstructed prose
- issuance and expiry
- stable event/nonce or other replay identity
- trust-root identifier and verification material appropriate to the chosen
  design

Unknown major, malformed fields, invalid time bounds, noncanonical repository,
bad SHA, missing action hash, changed argv, or unsupported outcome must refuse.
Optional additive fields may be preserved only according to the repository's
existing versioning law.

Add a versioned `GateExecutionClaimV1` (final naming follows repository
conventions) that binds the action ID/hash, PR/head, protected base ref/base
SHA/merge-base, approval receipt digest, claim time, and one-time execution
identity. Claiming must be an atomic append against audited anchored state. A
claimed action is permanently consumed: failure requires a fresh Gate
run/action, never replay.

Use existing `contracts.Verdict` and `ReviewPanelV1` for clean/pass,
escalate/address, and block evidence. `ReviewFindingsV1` remains the actionable
address-work contract and must never be stretched into a clean-pass artifact.
The approval receipt binds the canonical digest of the Gate evidence bundle and
the exact judgment question.

Contracts contain vocabulary and validation only. Gate owns authorization,
claim, and execution policy; the workflow is trusted transport and secret
release only.

### A2. Merge-time Gate decision and one-time claim

Add the smallest Gate CLI seam that, inside the protected run, mints a
short-lived head-bound grant from the verified approval receipt, evaluates the
live exact PR/head, resolves any provider-neutral judgment through Gate-owned
policy, and atomically claims only the newest terminal `would_merge` action.

Required behavior:

- Read through Gate's existing state/artifact APIs.
- Refuse an older superseded action.
- Refuse a moved or closed PR.
- Refuse a mismatched repo, PR, head, action hash, or merge argv.
- Never reconstruct the merge command.
- Never turn a parked, blocked, refused, malformed, or ambiguous run into an
  authorization.
- Refuse an approval receipt that does not bind the exact evidence digest,
  judgment question, PR, and head.
- Serialize the authoritative hosted state per repository; once an action is
  claimed, later Gate writes for that PR must refuse until execution records a
  terminal result.
- Re-audit anchored state and re-read live PR/head immediately before claim.
- Require the live base ref, base SHA, and merge-base to equal the approved
  values.
- Claim exactly once before creating any App installation token. Persist the
  claim with the separate GitHub Actions state-writer identity, whose branch
  rules permit updates only to `gate-state` and deny `main`.
- Use a non-force compare-and-swap ref update, then re-fetch and re-audit the
  durable `gate-state` tip containing the claim before the executor may receive
  the App private key.
- A claim or transport failure never turns into merge authority.
- The agent never supplies or handles mint or App credentials.

### A3. Custodied Gate App executor

Add a default-branch-only protected workflow that invokes a Gate-owned executor.
The executor consumes one audited claim and merges exactly the named PR; it does
not publish reusable success as a commit status.

The workflow must:

- run only trusted base-branch code;
- never check out or execute PR/fork code;
- request read-only workflow permissions; the default `GITHUB_TOKEN` has no
  merge or status authority;
- verify the run's approval-history receipt and immutable approving actor before
  any mint or App secret is used;
- verify exact repo, PR, open state, and current head;
- verify the claimed action is still the newest executable Gate outcome;
- verify expiry and replay identity;
- byte-compare the exact merge argv;
- pass the App private key only to the Gate executor process after the claim;
- exchange the private key for a short-lived installation token internally;
  never return, print, persist, or expose that token to another workflow step;
- execute only the stored argv, unchanged, including `--match-head-commit`, and
  never add `--admin`;
- serialize all executor runs for the repository so only the Gate App could
  move the approved base, then re-check base/head immediately before execution;
- append the execution result to anchored Gate state and refuse every replay;
- produce an auditable run summary without secrets;
- fail closed before merge on malformed, stale, ambiguous, unapproved,
  superseded, already-claimed, or transport-error input.

The App is the policy-custodied exact-command executor. A workflow step that
receives a general installation token is a rejected thin wrapper.

Layered rules are load-bearing:

- `main` Restrict updates: only the Gate App, PR-only bypass;
- `gate-state` Restrict updates: only `github-actions[bot]`;
- all other base-repository branches: normal human roles and explicitly
  approved existing integrations, but never the Gate App;
- `main` required `gate` check: separate rule with the same Gate App as its sole
  PR-only bypass actor.

Thus green commit status alone is insufficient, Actions cannot update `main`,
the Gate App cannot update a retargeted non-`main` base, and only the claimed PR
can be selected by the custodied executor.

### A4. Tests and adversarial pass

At minimum, pin:

- valid exact-head approval + newest action -> one claim and one exact PR merge;
- stale head;
- closed/merged PR;
- wrong repo or PR;
- malformed/unknown major;
- bad signature or missing protected approval;
- expired authorization;
- replay/duplicate delivery;
- older `would_merge` superseded before claim by block/park/newer run;
- newer run or duplicate execution after claim;
- forged full-SHA merge argv;
- changed merge method/flags/order;
- absent or ambiguous GitHub read;
- fork-controlled workflow/code attempt;
- shared-head/second-PR case cannot inherit authority;
- base retarget before and after claim;
- base SHA movement and merge-base mismatch;
- workflow permission and trusted-checkout shape;
- App key/token never crosses the executor process boundary;
- state writer can update `gate-state` but cannot update `main`;
- Gate App can update `main` through the claimed PR but cannot update any other
  base-repository branch;
- ambient user cannot update `main`, even when every status is green;
- exact stored argv succeeds under the App without `--admin`;
- token, merge, audit-write, and transport failures cannot create a second
  attempt or reusable success;
- no agent grant mint and no ambient-user merge side effect.

Run the scoped Gate checks and full repository checks:

```text
gofmt -l .
go vet ./...
golangci-lint run ./...
go test ./...
```

CI must be green. Add a fresh-context adversarial Codex review focused on
forgery, replay, TOCTOU, identity confusion, fork safety, and branch-protection
semantics. Address every actionable P1/P2 finding and re-review the exact head.

### A5. Bootstrap boundary

The executor cannot authorize its own first merge because its trusted code,
App, environment, and branch-rule authority are not armed yet. Do not disguise
this circular dependency.

After local green, CI, fresh exact-head Codex reviews, and a T3
provider-neutral Gate judgment, stop with:

- the exact bridge PR and head;
- the exact Gate run/action hash;
- proof of the current legacy required-check deadlock;
- an equivalent-or-stronger staged ruleset set that preserves every existing
  human protection and introduces no unprotected interval: sole-App PR-only
  updates to `main`, sole-Actions updates to `gate-state`, ordinary roles but
  not Gate App on other branches, and the required `gate` check with only the
  same Gate App receiving PR-only bypass;
- a disposable canary proving Gate's exact argv succeeds with the App
  installation token against an intentionally red `gate` check and without
  `--admin`, while ambient-user main update, Actions main update, and Gate-App
  non-main update all refuse;
- the smallest operator-only App/environment/ruleset/bootstrap actions;
- rollback steps;
- a statement of which permanent protections remain unchanged.

The operator chooses and performs the App registration, environment approval,
ruleset migration, and bootstrap. The agent never uses `--admin`, edits those
protections, or invokes missing Claude/Cursor reviewers without explicit new
authority.

### A6. Live proof and existing PR recovery

After the executor is on `main` and operator-armed:

1. Run one disposable valid exact-head approval/action through the App executor
   and prove exactly one PR merges with Gate's unchanged argv.
2. Prove stale, malformed, forged, expired, superseded, already-claimed, and
   replay cases cannot mint an App token or merge.
3. Re-read PRs #165 and #168.
4. Re-run Gate if either head or newest terminal decision changed.
5. Run each through the protected PR-specific executor.
6. Verify App identity, exact argv, claim/result artifacts, and GitHub merge
   facts agree.

Do not proceed to the auto-mode implementation until PR #168 is actually merged
and its merge fact is readable from GitHub.

## Phase B — Workbench auto-mode implementation

After #168 merges, consume its first manifest:

```text
/work-driver docs/features/codex-auto-mode-parity/driver.md --engine session
```

This is a Workbench-only five-stream parent run:

1. `auto-decision-v1-contract`
   Task `tsk_01KYP78RWR1HJ0P8KWEZ0QJMA2`

2. `session-reviewfindings-address-boundary`
   Task `tsk_01KYP81VP40DX668F3G6JCCKGR`

3. `codexguard-policy-engine`
   Task `tsk_01KYP78S45H49XBVAS8WWQPB1R`

4. `codexguard-hook-adapter`
   Task `tsk_01KYP78SCVDK6VTNS5QXQFZBVM`

5. `codexguard-policy-projection`
   Task `tsk_01KYP78SKH7AF7EE2J2Y9NTZA6`

The decision contract and session ReviewFindings boundary may run in parallel.
The policy owner, hooks, and projection land in dependency order. Every child:

- creates a child driver-state run;
- uses an isolated worktree from current `origin/main`;
- reads all scoped guidance;
- implements only its spec;
- runs documented checks;
- opens a focused PR;
- returns the structured session-driver handoff;
- receives fresh exact-head Codex review;
- merges only through the trusted Gate path and exact emitted command.

The operator-authorized run policy remains: no Claude/Cursor trigger and no
model-provider run/API key. Record configured-panel omissions explicitly; do
not silently pretend the panel completed.

## Phase C — Ship ReviewFindings conformance

After the Workbench session address boundary and its canonical corpus merge:

```text
/work-driver docs/features/codex-auto-mode-parity/ship-driver.md --engine session
```

Task:

- `reviewfindings-shared-conformance-corpus`
- `tsk_01KYPYX4EQTVC8S3Z1HAQXSZXK`

The Ship child vendors the exact Workbench corpus bytes and digest, then proves
its independent lifecycle consumer matches the shared accept/refuse,
consumption, and at-most-once projections. Consumer-specific effects remain
separate: Workbench address-work/claim state versus Ship provider-call count.

Before touching Ship, read its root and scoped guidance completely. Use Ship's
documented checks, including full `make check` and Ubuntu/Windows CI. Do not add
a Workbench runtime dependency or cross-repository call stack.

## Phase D — fresh-task live dogfood

After the Workbench and Ship implementation receipts are merged and verified:

```text
/work-driver docs/features/codex-auto-mode-parity/dogfood-driver.md --engine session
```

Task:

- `codexguard-fresh-task-dogfood`
- `tsk_01KYP78SSC77DRNABJB8F3VAVF`

The live proof must demonstrate:

- a fresh Codex task discovers and trusts the installed reviewed policy;
- safe reads/tests proceed with the intended low friction;
- bare merge refuses with the exact Gate remedy;
- a forged full-SHA Gate-shaped merge also refuses;
- only Gate's newest persisted command for the exact live head passes;
- grant mint, Gate-state mutation, custody mutation, `--admin`, force push,
  deletion, and visibility changes refuse;
- Bash, PowerShell, `cmd`, nested code-mode, local-function, and supported MCP
  envelopes cannot bypass equivalent rules;
- hook crash/timeout/malformed/untrusted behavior matches the honest
  rules-backstop-or-park matrix;
- Codex produces a real, exact-head `ReviewFindingsV1`;
- the session boundary accepts it once and creates reconstructable address work;
- a fresh Codex child updates the existing PR;
- stale, malformed, duplicate, exhausted, empty, unsourced, and inconsistent
  artifacts refuse before dispatch;
- evidence is replayable and secret-free.

## Phase E — closeout

When all implementation and dogfood PRs are merged:

- verify every parent/child driver-state ledger and closure receipt;
- close the seven implementation tasks and umbrella only from real receipts;
- append material friction to the private/operator friction log, not public docs;
- remove clean secondary worktrees according to the repository workflow;
- run `/shipped` for the final retrospective;
- leave a concise handoff containing:
  - capability gained;
  - exact fresh-task invocation;
  - artifact/schema paths;
  - bridge trust root and refusal semantics;
  - tests and PRs;
  - dogfood evidence;
  - remaining limitations;
  - any manual intervention still required.

## Later track — physical/passkey custody

The exact-command Gate App executor is now part of Phase A; PR #143's App-mint
TDD must be superseded or revised before implementation because its original
non-goal excluded execution and assumed commit-status enforcement was enough.

After the bridge and auto-mode program close, harden the independent approval
root with the physical-custody tap/passkey design. That later project may remove
the temporary second-account approval friction, shorten credential exposure,
and strengthen revocation without changing Gate's artifact or exact-command
contracts.

## Definition of done

The goal is complete only when:

1. Provider-neutral Gate authorization can safely drive one PR-specific,
   exact-command merge through the dedicated Gate App without Claude or a
   model-provider run.
2. Forged, stale, malformed, expired, replayed, superseded, or already-claimed
   authorizations cannot mint an App token or merge.
3. PRs #165 and #168 are merged through Gate's exact emitted commands.
4. All five Workbench auto-mode implementation streams are merged.
5. Ship passes the shared ReviewFindings conformance corpus and merges.
6. A real fresh Codex task completes the live dogfood, including one
   Codex-produced exact-head review artifact accepted once by the shared
   lifecycle boundary.
7. Driver ledgers, Gate runs, PR heads, merge commits, and closure receipts are
   mutually reconstructable.
8. No grant was agent-minted; the App key/token never left the custodied
   executor; no `--admin`, check weakening, hidden cloud model run, Claude
   dependency, or private material entered the public repository.

If a security choice, grant, protected-environment approval, App registration,
or bootstrap merge requires operator authority, stop with the exact request.
Never improvise past it.
