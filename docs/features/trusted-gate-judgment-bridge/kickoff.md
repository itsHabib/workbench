# Goal kickoff — trusted Gate judgment bridge, Codex auto-mode, and live dogfood

**Status:** ready for goal-mode execution
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
authorization become the required app-pinned `gate` status without making a
user-posted status authoritative.

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
credential authority. It explicitly does not make the App the enforcement
check. The judgment bridge in this goal is the smaller prerequisite that
unblocks merges now. Keep the two efforts separate and cross-linked.

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

- Never mint a grant. The operator alone provides grants.
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
- Ordinary GitHub Actions CI and the trusted status workflow are in scope; they
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
  -> operator-approved one-time bootstrap
  -> merge #165 and #168 through app-pinned gate
  -> Workbench five-stream session run
  -> Ship one-stream conformance run
  -> Workbench one-stream fresh-task dogfood
  -> close auto-mode umbrella
  -> full GitHub App credential authority later
```

## Phase A — trusted Gate judgment bridge

Create or reconcile one Dossier task:

```text
project: workbench
phase: codex-auto-mode-parity
slug: trusted-gate-judgment-bridge
title: Bridge provider-neutral Gate judgments into the required app-pinned status
```

Do not create a duplicate if an equivalent live task already exists.

Target one tightly coupled T3 implementation PR so only one bootstrap exception
is required. If the weighted scope cannot honestly stay reviewable, stop and
present the smallest split plus its bootstrap consequences before coding.

### A0. Audit and design decision

Audit the current Gate action artifact, provider-neutral judgment contract,
state/audit chain, stamp mechanism, hosted workflow, branch protection, and
GitHub status creator identities.

Choose and document the smallest trust root that the hosted workflow can
actually verify. Acceptable designs include:

- a canonical, signed, exact-head authorization artifact whose verification key
  is pinned for the trusted workflow; or
- an operator-approved protected GitHub environment that makes the promotion
  itself an authenticated operator action.

An opaque `repository_dispatch` payload, a user-authored commit status, a login
allowlist that agents can impersonate with the ambient operator token, or a
description containing an unverifiable hash is not sufficient.

If the proposed signing key is readable by the same untrusted agent identity it
is supposed to constrain, state that honestly and stop for a security decision.
Do not call convention-based custody cryptographic enforcement.

Record the decision in a focused design section or ADR in the bridge PR.

### A1. Versioned authorization artifact

Use or implement a versioned shared contract, tentatively
`GateAuthorizationV1`, under a leaf `contracts` package. Final naming should
follow existing contract conventions.

The canonical signed/approved content must bind at least:

- schema/version
- repository
- PR number
- exact 40-character head SHA
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

The contract contains vocabulary and validation only. Gate owns authorization
policy; the workflow owns transport and app-pinned status publication.

### A2. Gate producer/publish seam

Add the smallest Gate CLI seam that exports or publishes the authorization only
after an existing exact-head run is the newest terminal `would_merge` action.

Required behavior:

- Read through Gate's existing state/artifact APIs.
- Refuse an older superseded action.
- Refuse a moved or closed PR.
- Refuse a mismatched repo, PR, head, action hash, or merge argv.
- Never reconstruct the merge command.
- Never turn a parked, blocked, refused, malformed, or ambiguous run into an
  authorization.
- Publishing is idempotent by the artifact's natural identity.
- A publish/transport failure never rewrites the Gate decision.
- No grant minting or merge execution occurs here.

### A3. Trusted GitHub Actions consumer

Add a default-branch-only trusted workflow that consumes the artifact and posts
the existing required `gate` context as `github-actions[bot]`.

The workflow must:

- run only trusted base-branch code;
- never check out or execute PR/fork code;
- request least privilege (`statuses: write` plus only required read scopes);
- verify the selected trust root before any status write;
- verify exact repo, PR, open state, and current head;
- verify the newest Gate outcome represented by the artifact;
- verify expiry and replay identity;
- byte-compare the exact merge argv;
- post success only for a valid current `would_merge`;
- post failure/error or nothing in every ambiguous/malformed/stale case,
  according to a documented fail-closed matrix;
- serialize per repo/PR/head as needed;
- produce an auditable run summary without secrets;
- leave branch protection requiring the existing app-pinned `gate` context.

Do not introduce an alternate required context or an OR-by-convention between
`gate` and `gate/authorized`.

### A4. Tests and adversarial pass

At minimum, pin:

- valid exact-head authorization -> one `gate=success`;
- stale head;
- closed/merged PR;
- wrong repo or PR;
- malformed/unknown major;
- bad signature or missing protected approval;
- expired authorization;
- replay/duplicate delivery;
- older `would_merge` superseded by block/park/newer run;
- forged full-SHA merge argv;
- changed merge method/flags/order;
- absent or ambiguous GitHub read;
- fork-controlled workflow/code attempt;
- shared-head/ambiguous-PR case;
- workflow permission and trusted-checkout shape;
- status creator is GitHub Actions, not the ambient user;
- transport failure cannot create success;
- no grant mint and no merge side effect.

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

The bridge cannot authorize its own first merge because the trusted consumer is
not on `main` yet. Do not disguise this circular dependency.

After local green, CI, fresh exact-head Codex reviews, and a T3
provider-neutral Gate judgment, stop with:

- the exact bridge PR and head;
- the exact Gate run/action hash;
- proof of the current required-check deadlock;
- the smallest operator-only bootstrap action;
- rollback steps;
- a statement of which permanent protections remain unchanged.

The operator chooses the bootstrap. The agent never uses `--admin`, alters
branch protection, or invokes missing Claude/Cursor reviewers without explicit
new authority.

### A6. Live proof and existing PR recovery

After the bridge is on `main`:

1. Publish one disposable valid exact-head authorization and prove the trusted
   workflow posts required `gate=success`.
2. Prove stale, malformed, forged, expired, and replay cases cannot post
   success.
3. Re-read PRs #165 and #168.
4. Re-run Gate if either head or newest terminal decision changed.
5. Publish their valid authorizations through the bridge.
6. Confirm the required app-pinned `gate` status is green.
7. Execute only each Gate-emitted commit-pinned merge command.

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

## Later track — full Gate GitHub App credential authority

Do not absorb this into the merge-unblocker.

After the bridge and auto-mode program close, resume or supersede PR #143's
App-mint TDD with an explicit decision. That later project may:

- install a dedicated Gate GitHub App;
- authenticate head-bound grant minting;
- mint short-lived installation tokens after Gate authorization;
- execute merges as the scoped App identity;
- remove merge authority from the standing user credential;
- converge with physical/passkey custody.

Re-audit PR #143 before implementation because it predates provider-neutral
judgment, exact-head panel completeness, and this bridge.

## Definition of done

The goal is complete only when:

1. Provider-neutral Gate authorization can safely produce the required
   app-pinned `gate` status without Claude or a model-provider run.
2. Forged, stale, malformed, expired, replayed, or superseded authorizations
   cannot produce success.
3. PRs #165 and #168 are merged through Gate's exact emitted commands.
4. All five Workbench auto-mode implementation streams are merged.
5. Ship passes the shared ReviewFindings conformance corpus and merges.
6. A real fresh Codex task completes the live dogfood, including one
   Codex-produced exact-head review artifact accepted once by the shared
   lifecycle boundary.
7. Driver ledgers, Gate runs, PR heads, merge commits, and closure receipts are
   mutually reconstructable.
8. No grant was agent-minted; no `--admin`, check weakening, hidden cloud model
   run, Claude dependency, or private material entered the public repository.

If a security choice, grant, protected-environment approval, App registration,
or bootstrap merge requires operator authority, stop with the exact request.
Never improvise past it.
