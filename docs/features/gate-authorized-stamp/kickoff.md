# Kickoff — gate emits a provenance "stamp" on a PR

**Status: exploration / POC.** Handoff brief, not a TDD — no rollout plan, no design gate.
Trust the `file:line` anchors below but re-verify before building on them.

## Goal (one line)

When `gate` authorizes a merge (exit 0), post a **provenance stamp onto the PR** — a GitHub
**commit status** (`gate/authorized` → success) plus an optional receipt comment — carrying
the decision's `run` id and the deciding artifact's chain `hash`, so the PR page visibly says
"gate authorized this, here's the verifiable receipt." The stamp **reflects** the decision;
it never creates it.

## The framing the operator is chasing

Gate's verdict is real but *invisible* — it lives in a local hash-chained log; the PR page
shows nothing until you run `gate next` or open console. This drags the verdict onto the PR
surface where the work lives (the same move console's READY TO MERGE section makes, pushed one
surface further out onto GitHub).

**Two hard invariants — the whole point is honesty, so these are load-bearing:**

1. **Strictly downstream of the decision.** The stamp is a *side effect* of a pass, gated on
   exit code `codeMerge (0)`. It must never touch the decision logic in `act`. The audit chain
   stays the single source of truth; the stamp is a legible pointer to it.
2. **A commit STATUS, never a GitHub review "approve".** gate *parks* (`parked_for_judgment`)
   when the head has no GitHub review decision. If the stamp were a self-approval, it would
   manufacture the exact signal gate reads to judge readiness — the stamp gaming its own gate.
   A commit status does not feed the review-decision signal, so it's the honest rail. This is
   the reason the operator's first instinct ("approve the PR with our token") is the one form
   to avoid.

Honest caveat to state plainly: posted with the operator's ambient `gh` token, the stamp is a
**legibility** marker, not a security control — forgeable by anyone with that token. Fine, as
long as it never *becomes* the authorization (that stays exit-0 + the chain).

## How gate's post-decision path works today (anchored)

**All CLI + decision logic is in `cmd/gate/main.go`** (`internal/` holds evidence, the verify
ladder, the reducer, state).

- **`act` (`main.go:532`) is the single decision function.** It maps the reduced verdict to an
  exit code: block→1 (`:570`), escalate/ceiling→parked 2 (`:574-629`), capability fail→refused
  3 (`:563`), and **PASS falls through at `main.go:631-642`** — dry-run returns `codeMerge (0)`
  / `would_merge` (`:637`), live returns `codeMerge (0)` / `merge_not_implemented` (`:641`).
- **Exit codes** at `main.go:51-57`: `codeMerge=0, codeBlocked=1, codeParked=2, codeRefused=3,
  codeError=4`.
- **The merge command** gate prints is built at `main.go:634-635`
  (`gh pr merge %d -R %s --squash --delete-branch --match-head-commit %s`, using
  `reduced.Subject.HeadSHA`), stored on `res.Action`. Gate never executes it.
- **`gate judge`** (`cmdJudge`, `main.go:784`) resolves a parked escalation and calls the **same
  `act`** (`main.go:854`) — so both the direct-pass and judge-then-pass paths flow through one
  fall-through.
- **No post-decision network side effect exists today.** The only "after deciding" effects are
  the local state append (`record(...)` closure, `main.go:542-558`) and `printJSON` (`:377`).
  The natural slot for a stamp is **in `cmdGate`/`cmdJudge` after `runGate`/act returns, gated on
  `code == codeMerge`, around `os.Exit` (`main.go:373-378`, `:854-859`)** — never inside `act`.
- **gate has NO GitHub write path.** Every `gh` shell-out is a read (`evidence.go:285-294`;
  `main.go:991,1018`). A stamp is gate's **first write to GitHub**.

### What the stamp should carry (verifiable payload)

A gate decision spans two chained rows in `state`'s append-only `log.jsonl`, wrapped in
`state.Artifact` (`state/state.go:59-68`: `ID, Kind, Run, Time, Parents, Body, Prev, Hash`):
the reduced **verdict** artifact (subject repo/PR/head_sha, decision, tier —
`contracts/verdict.go:22-39`) and the **action** artifact (outcome, grant, merge command).

Minimal identifiers that verifiably pin the stamp to the chain:

1. **`run`** — `run_<16hex>` (`Artifact.Run`, minted once per invocation at `main.go:403`;
   surfaced in `gateResult.Run`). Selects the decision's artifact group (`gate explain -run`).
2. **`hash`** — the deciding **action** artifact's `Hash` (`state.go:67`; computed by
   `hashArtifact`, `state.go:513-521`, over id|kind|run|time|prev|parents|body). This is the
   tamper-evident anchor — the single field that makes the stamp *verifiable*, not decorative.
3. **`head_sha`** (optional) — `reduced.Subject.HeadSHA` (`main.go:540`), binds the stamp to the
   exact commit judged (a live head can move; a verdict is only valid for its head). Also the
   SHA the status must be posted against.

Verification path a skeptic runs: `gate audit` (replays chain + checks the keyed anchor,
`state.go:402-433` / `anchor.go`) → `gate explain -run <run>` → confirm the `action` artifact's
`Hash` matches the stamped hash and its outcome is `would_merge`/`merged`. `run` alone isn't
tamper-proof (a rewrite can preserve run ids); the **`hash` + `gate audit`** together are what
make it certifiable.

## The one architectural fork (the crux of the design)

**The commit-status POST rail already exists — but in the CI workflow, not the Go binary.**

- **Workflow rail (exists):** `.github/workflows/gate.yml:261-265` already does
  `gh api -X POST repos/${REPO}/statuses/${HEAD_SHA} -f state=... -f context=gate ...`, running
  in the trusted `workflow_run` context under `permissions: statuses: write` +
  `GH_TOKEN: github.token` (`gate.yml:15-19,34-35`). It is **dormant** — gated on
  `vars.GATE_ENFORCE == 'true'` (`gate.yml:31`), the canary that ships off. Adding a second
  context (`gate/authorized`) here is a cheap workflow addition — but it **only fires in armed
  CI**, not in the operator's live local flow.
- **Binary rail (net-new):** gate's Go code posting the status directly, downstream of the
  `codeMerge` return. No Go status-write plumbing exists today; the binary relies on ambient
  `gh auth`. This is what makes the stamp show up in the operator's **actual current loop** —
  the hand-driven `gate.exe … → gate judge … → gh pr merge` tail (cloud is down; everything is
  hand-run today). Recommend **this** for the POC.

So the real decision: **binary-emit (works in the live hand-driven flow now) vs workflow-emit
(only in armed CI later).** Given today's reality, build binary-emit; note gate.yml as the
eventual armed-CI home so the two don't drift into parallel truths.

## Weak spots / things to push on

- **Self-reference trap.** Gate's readiness reads the PR's full commit-status rollup and treats
  any non-green context as a block (`docs/enforcement.md:266-275`). A new `gate/authorized`
  context becomes visible to that rollup. Keep it **success-only** (only ever posted on a pass,
  never pending/failure) and/or **exclude it from gate's own rollup filter**, or gate could end
  up blocking on the presence of its own stamp. Verify how the rollup filter treats unknown
  contexts before shipping.
- **Idempotency / re-runs.** Running `gate gate` twice on the same head re-posts. A commit status
  on the same `(sha, context)` overwrites — fine. A receipt *comment* would duplicate — dedupe by
  editing an existing gate comment (marker string) rather than always appending.
- **Token custody.** Binary-emit uses the operator's ambient `gh` token — the same shared
  merge-capable credential (`docs/enforcement.md:43-70`), no separate stamp-only identity. Fine
  for local/personal; name it in any hardening pass.
- **Live-vs-dry outcome wording.** On a pass, live currently records `merge_not_implemented`
  (`main.go:641`) — the stamp should key off exit code `0`, not the outcome string, so it fires
  identically in dry-run and live.
- **Comment vs status.** The status is the load-bearing legible marker (it rides the rail branch
  protection already watches). The receipt comment is optional sugar — decide if it earns its
  keep or if `target_url` → `gate explain` output is enough.

## Phase-0 spike (do FIRST, report before committing)

**Confirm the operator's ambient `gh` token can POST a commit status to the target repo.** The
binary-emit path relies on ambient `gh auth` (no dedicated token plumbing). One command:

```sh
gh api -X POST repos/itsHabib/workbench/statuses/<some-sha> \
  -f state=success -f context=gate/authorized -f description="smoke test"
```

If it succeeds → binary-emit is viable, proceed to wire it downstream of `codeMerge`. If it
403s (token lacks `statuses: write` / repo scope) → that's the integration gap; fall back to the
workflow rail or fix the token scope. Also decide the binary-vs-workflow fork here (recommend
binary for the live flow).

## Success metric (gradient)

1. **Baseline:** after a real local `gate gate` pass, the PR shows a `gate/authorized` success
   status whose `target_url`/description carries `run` + `hash`; a blocked/parked/refused run
   posts **nothing**. *(In reach once the spike clears.)*
2. **Verifiable:** a skeptic takes the stamped `run`+`hash`, runs `gate audit` + `gate explain
   -run`, and confirms the stamp maps to a real, chain-consistent passed decision.
3. **Integrated (later):** the same context posted from armed CI (`gate.yml`, GATE_ENFORCE on)
   without a second source of truth — the binary and workflow agree on the context + payload.

## POC results (2026-07-26, binary-emit)

Built and verified end-to-end. Uncommitted on the working tree.

- **Phase-0 spike cleared.** Ambient `gh` token (`repo` scope) POSTs a `gate/authorized`
  commit status — no 403. Binary-emit is viable; no token-scope gap.
- **Self-reference trap: needed a readiness exclusion (two vectors).** Success-only handles the
  *block* vector (`green()` treats `state=SUCCESS` as green, so a `gate/authorized` entry never
  blocks). But PR #134 added a second vector: readiness counts *effective (non-`gate`) checks*
  to fire the empty-CI escalation, and `gate/authorized` is not the exact `gate` context — so a
  posted stamp would count as real CI and let a later run pass a head with no CI (a fail-open
  codex caught, P1). Fix: `isOwnGateStatus` now also excludes `gate/authorized` from both the
  block loop and the effective-CI count, pinned to `stamp.Context` by a drift test.
- **Code.** `cmd/gate/internal/stamp/` (new mechanism package — `gh api` status POST, stdlib
  only, no tenant import). `gateResult.Hash` added; the pass path in `act` surfaces the action
  artifact's chain hash. `emitAuthorizedStamp` fires at every merge-authorizing entry point —
  `cmdGate`, `cmdJudge`, and `cmdResolve` (the escalation resolution back-channel added by
  #130) — gated on `code == codeMerge`, best-effort (a post failure warns on stderr, never
  changes the exit code); it derives the repo from `res.PR` so there is one source, not a
  separate repo arg per call site. `-stamp` flag (default on) is the opt-out for gate's
  first-ever GitHub write.
- **Payload.** `context=gate/authorized`, `state=success`, `description="gate authorized ·
  run=<run> hash=<sha256>"` (fits the 140-char ceiling), `target_url=<commit page>`, posted
  against `head_sha`.
- **E2E (throwaway state tree, merged PR #126):** park → **no stamp** (exit 2); judge pass →
  exit 0 → status live on the head carrying run+hash. Skeptic path confirmed: `gate audit` →
  "chain intact", and the stamped hash is exactly the run's `action` artifact (`would_merge`).
  Success gradient #1 + #2 met. #3 (armed-CI parity via `gate.yml:261-265`) still open.
- **Verification path made real.** `gate explain -run` now surfaces each artifact's chain
  `hash` (text + JSON; `observe.Node` had dropped it), so a skeptic can read run+hash off the
  stamp, run `gate explain -run <run>`, and match the deciding action node's hash directly —
  the receipt is now verifiable through the documented CLI, not just by raw state inspection.
- **Checks.** gofmt/vet/golangci-lint/`go test ./cmd/gate/...` all green (incl. the refreshed
  `explain.golden`).

### Known limitation — commit-scoped rail (accepted)

A `gate/authorized` **commit status** is commit-scoped by nature: it rides the exact rail branch
protection watches, which is why it's the load-bearing surface (a PR comment can't gate a
merge). The tradeoff is that once posted, a PR *opened or retargeted onto the same head SHA
later* inherits the status — the pre-post "sole open PR on the head" guard cannot see a PR that
does not yet exist. This is not fixable without leaving the branch-protection rail, so it is
**accepted and mitigated, not prevented**:

- The stamp is **self-identifying** — `description` carries `#<num>` and `target_url` points at
  the exact PR gate authorized — so a viewer on an inheriting PR sees which PR the stamp is for,
  not a bare green.
- The stamp is a **legibility marker, not a security control** (stated up front): the
  authorization is the exit code + the hash-chained log, never the status. An inherited stamp
  authorizes nothing; it only points at the one real decision, verifiable via `gate explain -run`.

A future hardening pass could add a PR-scoped receipt *comment* alongside the status, or reconcile
stale/inherited stamps — out of scope for this POC.
