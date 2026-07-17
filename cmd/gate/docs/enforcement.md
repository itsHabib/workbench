# gate — enforcement model

Status: v0
Date: 2026-07-06

This document states, plainly, **what actually forces a merge through the
gate** and what does not. It is deliberately kept separate from
`docs/DESIGN.md` (whose *Tamper model* section owns the state-log integrity
boundary): this file owns the merge-enforcement boundary.

The one-line summary: **today the capability plane is discipline plus an audit
trail, not prevention.** The gate becomes *enforcing* only once the target
repository's branch protection requires the `gate` status check. Everything
below expands on that sentence and names the gaps honestly, because the
alternative — implying the gate bounds what it cannot — is the exact failure
this tool exists to correct.

## What forces merges through the gate: branch protection

The gate is a decision procedure. It reads evidence, runs the ladder, and
records an outcome. Nothing in the gate reaches out and *prevents* a merge;
by itself it produces a verdict and an artifact, not a locked door.

The forcing function is **GitHub branch protection on the target
repository**. A merge is bound by the gate only when the protected branch:

1. **requires the `gate` status check** to be green before a PR can merge, and
2. **disallows administrator bypass** of required checks, and
3. **disallows direct pushes** to the protected branch (so the branch can only
   advance through a checked PR merge).

Until all three are configured, the gate bounds nothing. An agent — or a
person — with merge rights can merge a PR the gate never saw, or one the gate
blocked, and the only trace is that no corresponding `action` artifact exists
in the log. That is an *audit trail after the fact*, not prevention. Name it
for what it is: **discipline plus an audit trail** until branch protection is
turned on.

This is the same "absence reads as green" hazard the reducer work fights
inside the gate, applied to the gate's own perimeter: an unprotected repo is a
repo where skipping the gate is silent.

## Token custody: the merge-capable identity must not be the bounded one

Branch protection decides *which* checks must pass. Token custody decides *who
holds the credential that can actually land the merge*. Both must hold for the
gate to bound anything.

**The intended model:**

- The **gate — or a CI identity wired to it** — holds the only token permitted
  to land a merge on the protected branch. When the gate reaches a `would_merge`
  outcome within the grant ceiling, that identity (and only that identity)
  performs the merge.
- The **agents the gate governs** hold tokens scoped so they can *push
  branches and open PRs* but **cannot merge** into the protected branch. Their
  path to a merge runs only through the gate's decision.

The separation is the whole point: the identity a merge flows through must not
be the identity the gate is meant to bound. If the bounded agent also holds a
merge-capable token, the gate is advisory to that agent by construction.

**On the current single-box setup this separation is not real.** Every local
agent shares one `gh` credential, and that credential can `gh pr merge`. There
is no distinct, merge-only CI identity yet; there is no scoping that stops a
governed agent from merging directly. Closing this — provisioning a merge-only
identity for the gate and demoting every governed agent's token to
push-and-open-PR-only — is a **precondition for `-live`**. Wiring live merge
execution before token custody is real would hand the gate a merge capability
that the agents it governs also hold, which defeats the bound.

## Mint authority and where the mint key lives

A grant is what authorizes an *effectful* verb (a merge, or a judgment that
resolves an escalation). Minting a grant is therefore the policy surface that
says "this identity may cause merges up to tier T within this window."

**Who/what may mint today: anyone who can run `gate`.** `gate grant` mints a
signed grant; there is no authentication gate in front of it. The
`MintedBy` field on a grant is a **free-form string with no authentication** —
`gate grant` records `"operator"`, `gate backtest` used to record
`"backtest"`, and nothing verifies either. It is provenance-as-claimed, not
provenance-as-proven. Treat `MintedBy` as a label, never as an authorization
fact. Adding real mint authentication (so that only a designated identity can
mint a spendable grant) is future work, not built here.

**Where the mint key lives.** Grants are HMAC-signed with `grant.key`. The
custody decision — that `grant.key` (and the anchor key) live **outside the
state directory**, so that an actor who can write `log.jsonl` cannot thereby
forge grants — is the sibling tamper task's decision. This document does not
re-specify the path; see `docs/DESIGN.md` *Tamper model* / *Trust boundary*
and `docs/FOLLOWUPS.md` for the key-custody decision and its default location.
The relevant fact for *enforcement* is the trust boundary it draws: signing
capability is not co-located with log-write capability. It is **not**
cryptographic non-repudiation — an actor who can read the key directory can
still forge grants (and the anchor).

## The bypass, named

**An agent with `gh pr merge` rights bypasses the gate entirely.**

The gate's own sanctioned merge path is a `gh pr merge ... --squash
--delete-branch --match-head-commit <sha>` command (dry-run prints it; `-live`
today records `merge_not_implemented`). But that same `gh pr merge` verb is
available to anyone holding a merge-capable `gh` token, run directly against
the PR, with no grant, no verdict, and no artifact. On the current single box,
where every agent shares one merge-capable `gh` credential, this bypass is
open by default.

This is stated here rather than implied-closed on purpose. The bypass is
closed only by the two mechanisms above acting together: **branch protection**
(so a direct merge is rejected unless the `gate` check is green) and **token
custody** (so the bounded agents do not hold a merge-capable token in the
first place). Neither is configured yet.

## Operator action — the `-live` precondition

This is a repository-settings action the **operator** performs; it is not code
in this repository and is not performed by this change.

> **Enable branch protection on the target repository(ies) requiring the
> `gate` status check — with admin bypass disallowed and direct pushes to the
> protected branch disallowed — before any `-live` wiring.**

This is the gating precondition recorded for the record: `-live` merge
execution must not be wired until (a) branch protection above is enabled on
the target repo(s), and (b) token custody is real (a merge-only identity for
the gate; governed agents demoted to push-and-open-PR-only). Until both hold,
the gate is discipline plus an audit trail, and this document is the honest
statement of that scope.

## Enforcement on the canary: the `gate` status check

The `.github/workflows/gate.yml` workflow turns the verdict above into a
`gate` commit status the canary's branch protection can require. Its shape,
and why each choice is the safe one:

- It triggers on **`workflow_run`** completed for the `CI` workflow — not
  `pull_request`, and not `pull_request_target`. `workflow_run` runs in the
  **trusted base-repo context** with a token that can post a commit status,
  even for a fork PR, *without checking out or executing any fork code*. That
  is the fork-safe pattern: a fork cannot exfiltrate a secret or subvert the
  run, because its code never runs in the privileged context. It also fires
  only after CI has settled.
- It checks out the **default branch (base), never the PR/fork head** — the
  checkout has no `ref:`. gate is therefore built from base, so a PR cannot
  edit gate's own source to neuter the check that governs it.
- Before running gate it **waits (bounded) for every non-`gate` check to
  settle**. gate reads the PR's status rollup and treats any non-green check —
  pending, queued, in-progress — as a block (fail closed); reading mid-flight
  CI would block spuriously. On timeout it proceeds anyway, which can only make
  gate block, never pass.
- It runs gate as a **dry run (never `-live`)** against an **ephemeral,
  per-run state + key dir** (`mktemp -d`), mirroring the backtest
  `newEphemeralEnv` precedent. No signing secret is stored in CI: the grant is
  minted and spent inside the throwaway dir and erased with it.
- The grant is minted `-max-tier T3 -max-cycles 0`. The check is meant to
  reflect gate's **ladder verdict** (block / escalate / pass), not a tier or
  cycle ceiling: a CI runner has no operator to re-mint a wider grant or judge
  an escalation, and per-run ephemeral state makes cross-run cycle counting
  meaningless. So the ceilings are opened wide and the check stands or falls on
  the ladder alone.
- It maps gate's exit code to the status **fail-closed**: `state=success` only
  for exit 0 (`would_merge`); block, park, and refuse all post `state=failure`;
  any other code posts `state=error`. If any earlier step fails (build, PR
  resolve, a gate crash), the job stops before the status is posted — the
  `gate` status is then absent, and branch protection blocks. No path posts
  success unless gate genuinely exited 0.

### Operator runbook — arming the canary

These are **repository-settings actions the operator performs** on the canary
repo; they are not code in this repository, and this change does not perform
them. (First armed on the standalone itsHabib/gate; since the `cmd/gate` tenant
move the workflow ships here, so the armable canary is itsHabib/workbench.)

0. **Resolve the runtime, then arm the workflow (it ships dormant).** The `gate`
   workflow is guarded by repo variable `GATE_ENFORCE` — it posts and enforces
   nothing until that is set to `true`, because gate's ladder needs `triage-floor`
   + a model backend a stock GitHub-hosted runner lacks (see "Known residuals").
   First decide the runtime — point gate's model rungs at a cloud model API (so it
   runs on a plain hosted runner), or use a box-side runner that has the deps —
   then `gh variable set GATE_ENFORCE --body true --repo itsHabib/workbench`.

On the protected branch (`main`):

1. **Register the check once.** GitHub only offers a status check as a
   *selectable* required check after it has reported at least once. So open one
   PR against `main` first and let the `gate` workflow post its status — that
   registers the `gate` context so it appears in the required-checks list.
2. **Require the `gate` status check.** In branch protection for `main`, enable
   "Require status checks to pass before merging" and select the **`gate`**
   context. A PR then cannot merge while `gate` is red or absent.
3. **Disallow bypassing.** Enable "Do not allow bypassing the above settings"
   so the required check binds administrators too — an admin cannot merge past
   a red or missing `gate` check.
4. **Disallow direct pushes to `main`.** Require pull requests before merging,
   so the branch can only advance through a checked PR merge, never a direct
   push that never faced the check.

Until the workflow is armed (step 0) and all four settings are set, the canary
is still discipline plus an audit trail — the workflow is dormant and posts
nothing.

## What this closes on the canary, and what stays open

With the runbook above applied on the canary repo, this change **closes the
direct-merge-without-gate bypass on the canary**: a merge to `main` requires
the green `gate` check, and with admin bypass disallowed that holds even for an
administrator. The named bypass above is shut on this one repo.

What it does **not** close — stated plainly, because implying otherwise is the
failure this document exists to correct:

- **Token custody stays open.** Every local agent still shares one
  merge-capable `gh` credential; there is still no distinct merge-only CI
  identity, and no scoping that stops a governed agent from *attempting* a
  direct merge. Branch protection with admin bypass disallowed still *rejects*
  such a merge for lacking the green `gate` check — so the perimeter holds on
  the canary — but the custody separation the intended model calls for is not
  built. It remains a `-live` precondition.
- **Mint authentication stays open.** `gate grant` is unauthenticated and
  `MintedBy` is a free-form, unverified label (see above). The CI check mints
  its own throwaway grant, so this does not weaken the check — but it does not
  add mint authentication either.
- **`-live` merge execution stays open.** The gate's sanctioned merge is still
  a dry run: `-live` records `merge_not_implemented`, unchanged by this work.
  The check evaluates `would_merge`, not an executed merge.

Three CI-context choices worth naming, since they shape what the check does and
does not assert:

- **It evaluates gate's full ladder.** The `gate` status is green only on a
  clean `would_merge`; a block or an escalation reads as a failing check. So it
  *composes with* the repo's other required checks (CI must be green for gate
  even to pass readiness) and is strictly *stricter* than them — it can fail a
  PR whose other checks are all green.
- **It uses an ephemeral per-run state + key dir.** No signing secret lives in
  CI, and cross-run cycle-capping is therefore *intentionally not enforced* by
  this check — each run counts zero prior cycles. Cycle ceilings remain an
  operator-durable-state concern, not a CI-check one.
- **Fork PRs are handled via `workflow_run`.** The trusted base context posts
  the status for fork PRs too, with no secret ever exposed to fork code.

### Known residuals of the CI check

Two are fail-closed (they can only make the check block, never wrongly pass);
one is a runtime dependency the check needs before it is functional in GitHub
CI. Named here rather than implied-closed.

- **The check needs gate's ladder dependencies present on the runner — not yet
  wired for a hosted CI runner.** gate's floor rung shells the `triage-floor`
  binary (which lives outside this module), and its review-consolidation rung
  calls a local model at `localhost:11434`. On the operator's box both are
  present, so the check runs its full ladder there. A stock GitHub-hosted runner
  has neither: with no `triage-floor` on PATH gate exits `error` (4) before it
  prints a verdict, and with no local model the review rung escalates every PR —
  so a clean PR could never reach a green `would_merge`. Making the check
  functional on a hosted runner therefore requires provisioning those
  dependencies (or a runner that has them); until then the workflow's fail-closed
  posture holds (a broken run posts `error`, never `success`), but the green path
  is unreachable in hosted CI. This is a live design question, not a settled
  choice — it is called out so the runbook above is not read as "already
  enforced on hosted CI."
- **gate's readiness reads its own `gate` status (self-reference).** Readiness
  reads the PR's full status rollup, which includes the `gate` context this
  workflow posts. A prior `failure`/`error` `gate` status on the same head SHA
  is therefore a non-green check gate will itself block on when it re-runs — so a
  transient gate failure does not clear until a new commit pushes a fresh head.
  This is fail-closed (a stale red gate can only keep blocking, never wave a PR
  through) and clears on the next push. The full fix is to have readiness skip
  the rollup entry whose context equals gate's own — deferred until the runner
  dependency above is settled, since the runtime shape may change what the check
  reads.

## See also

- `docs/DESIGN.md` — the artifact contract, the verdict schema, the ladder
  law, and the *Tamper model* (state-log integrity, key custody, trust
  boundary).
- `docs/FOLLOWUPS.md` — "Write down and enforce the capability backstop" and
  the sibling hardening items.
