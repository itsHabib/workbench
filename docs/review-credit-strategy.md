# Review-credit strategy — measured opt-in canary (v3, 2026-07-30)

Status: implementation canary. Phase 0 measurement remains, but the earlier
30-day prerequisite for any routing is superseded by the explicitly enabled
personal-repository canary in
[`features/tier-aware-review-canary/`](features/tier-aware-review-canary/plan.md).
Repositories without that opt-in—including employer/work repositories—retain
the full safe panel. Wider expansion remains gated on the live evidence and
rollback criteria in the
[`canary report`](features/tier-aware-review-canary/canary-report.md).

## Problem

Review spend is the first thing to exhaust monthly credits. Current process is
"blast all reviewers on every PR": 4 bots (claude, cursor, codex, copilot) ×
up to 3 cycles, on ~300 PRs/30d. Least mature part of the workflow — either
confirm it's right or route by risk; at minimum instrument to get the data.

## Established facts

- **Volume (PRs opened since 2026-06-20):** workbench 84, ship 85, roxiq 36,
  rooms 35, gate 21, dossier 8, cc-skills 7, tracelens 6, hooks 5, others <5.
  ~300 total, ~370k LOC churn.
- **@claude reviews bill the Max subscription.** Every repo's
  `.github/workflows/claude.yml` uses `secrets.CLAUDE_CODE_OAUTH_TOKEN` — the
  same pool as interactive sessions and ship local-claude driver runs. This is
  the pool that runs dry. Cursor Bugbot = Cursor credits, auto-fires on all
  repos (account-level app, no in-repo config). Codex = ChatGPT sub
  (comment-triggered). Copilot = Copilot sub (`--add-reviewer`).
- **Caveat that reshapes the whole strategy: the sweep counts *events*, not
  *tokens*.** A diff review and a ship local-claude driver run bill the same
  pool at wildly different sizes. "claude is the most active reviewer" proves
  claude reviews are frequent, not that they are the drain. If reviews turn
  out to be, say, ~15% of pool spend, cutting them ~70% saves ~10% and the
  real intervention target is driver runs. Settling this is Phase 0's job.
- **The review recipe is prose, not code:** `ship/CLAUDE.md` "Shipping
  Features" — add copilot reviewer, comment `@codex review` + `@claude
  review`, cursor fires itself, repeat 3 cycles. No code gates it; changing
  policy = editing that prose (+ /work-driver policy memory).
- **A risk-router already exists and is validated but unwired:**
  `workbench/cmd/triage` — `triage-floor` (deterministic tier from diff,
  T0 auto / T1 standard / T2 sensitive / T3 critical, fail-closed) +
  `triage-advisory` (escalate-only). Both binaries on PATH
  (`~/go/bin/triage-floor`). Spec:
  `workbench/cmd/triage/docs/features/pr-risk-engine/spec.md`. Core bet:
  review load scales with risk, not PR count. Driver→pr-risk wiring is
  planned-not-built; `/pr-risk` is hand-invoked, recommend-only.
- **The floor's documented blind spot is gate machinery.** RUBRIC's held-out
  eval (HELDOUT-01) found 15 under-calls, 8 of them gate-machinery — merge
  gates, verifiers, driver paths the floor reads as "internal → T1". Under
  blast-everything that was harmless (the panel was the compensating
  control); any policy that routes on the floor alone inherits it.
- Instrumentation seams that already exist: review-coordinator JSON verdict,
  `revtriage.exe -json` (local digest). `labels/mismatches.jsonl` exists but
  is the classifier's oracle — RUBRIC §5.4 floors any `labels/**` edit at T3.
  **The spend log must be a fresh file, never an extension of it.**

## Data sweep — COMPLETE (results below)

Retro-classified every PR since 2026-06-20 across 12 repos with
`gh pr diff N | triage-floor -v`, plus actual bot review submissions and
inline bot comments per PR. Raw TSV: `pers/docs/review-sweep-2026-07-20.tsv`
(repo, pr, tier, signals, bot_reviews, bot_inline_comments).

**299 PRs / 30 days. Tier distribution (triage-floor, deterministic):**

| Tier | PRs | % | avg bot reviews | avg inline comments |
|---|---|---|---|---|
| T0 auto | 85 | 28% | 1.8 | 3.2 |
| T1 standard | 127 | 42% | 1.8 | 2.3 |
| T2 sensitive | 73 | 24% | 2.6 | 4.4 |
| T3 critical | 14 | 5% | 3.5 | 7.8 |

Totals: 622 bot review submissions, 990 inline comments in 30 days.

**Headline: review effort barely differentiates by risk.** T0/T1 (71% of PRs)
average 1.8 reviews vs 3.5 on T3 — a 2× spread where the risk spread is far
wider. Spend is driven by PR count, exactly what the triage spec predicted.
Encouraging validation: bots naturally comment ~2.4× more densely on T3 than
T1, so the floor's tiers correlate with where findings concentrate. But note
the mass: 85×3.2 + 127×2.3 ≈ **564 of 990 inline findings (57%) land on
T0/T1** — density favors T2/T3, mass does not. Any T0/T1 defunding trades
away real findings; the honest claim is "spend down, loss unknown,
instrumented to find out", never "zero loss". The TSV holds named
counterexamples (ship#172: T0 policy doc, 6 reviews / 9 comments) and
data-quality outliers to resolve (workbench#13: T0 with 24 reviews;
workbench#51 / roxiq#134: `files=0` yet T0 — should hit fail-closed).

**Per-bot activity (review submissions + comments, top 5 repos = 261 PRs):**

| Bot | events | notes |
|---|---|---|
| claude[bot] | 397 | ~1.5/PR — most active by events, 100% Max pool |
| codex | 393 | ChatGPT sub — effectively tied with claude on engagement |
| cursor[bot] | 362 | Cursor credits, auto-fires on every PR |
| copilot | 142 | Copilot sub — cheapest, and least engaged |

Events ≠ efficacy and events ≠ tokens (see Established facts). This table
justifies *instrumenting* claude's review cost, and it picks the T1
single-reviewer default for Phase 1: **codex** (393 events), not copilot
(142) — "cheapest adequate" must not select the least-engaged reviewer.

## Phase 0 — measure + deterministic guardrail (build now, no-regret)

Nothing here changes who reviews what. It makes the system able to answer
"what do reviews actually cost, and what would a cut actually lose" — and it
closes the floor's known blind spot so Phase 1 has a safe signal to route on.

1. **Driver carries the tier — per head, not per PR.** Ship's driver
   classifies each stream's PR via `triage-floor` at PR-observe time. The
   tier is bound to the classified `head_sha` and recomputed whenever the
   head moves (fix commits from later review cycles can change the diff's
   risk class — a T1 PR that grows a gate-machinery fix must re-tier);
   each `review_cycle` event carries the tier of *its* head, and any
   routing always reads the current head's tier. Persists on the stream,
   shows in status/render. Mechanism only.
   **Classifier failure is its own state, never a fabricated tier:**
   missing binary, exit 1, or garbage output records
   `tier_source:"classifier_error"` (with no tier), warns, and routes at
   the full-panel posture — today's behavior, strictly stronger than any
   tier's route. A broken classifier must not silently take T2's weakened
   route, and the spend log must not launder failure cycles into T2
   statistics; classified heads record `tier_source:"classified"`.
2. **Per-repo path overrides lift gate/driver/merge machinery,
   deterministically — two bands, not one.** One rubric-shaped table in
   triage, floor + overrides, not floor alone. The table splits by
   consequence: **merge-authorization and signing paths floor at T3**
   (workbench `cmd/gate/internal/state/**`, `verify/**`, grant/anchor/
   exit-code machinery — HELDOUT-01's own labels put gate#3/#5/#9 at T3,
   and under the parked mapping T2 would still drop @claude and the
   adversarial pass exactly where a fail-open matters most); **broader
   gate/driver machinery floors at T2** (ship `packages/driver/**`, the
   rest of `cmd/gate/**`, `cmd/triage/**`). Repo identity enters through
   a new `-repo owner/name` flag on `triage-floor` (today's seam is
   stdin-diff only): callers that know the repo — driver, recipes, gate —
   pass it; absent flag ⇒ overrides skipped and behavior is byte-identical
   to today, so nothing global ever applies another repo's globs.
   Overrides only raise, never lower; `-v` names each override hit. This
   is the compensating control for HELDOUT-01's gate-machinery
   under-calls, cheap and testable now, useful regardless of Phase 1's
   fate.
3. **`review-spend.jsonl` — a fresh file** (ship state dir, same convention
   as the store; never `labels/**`). Append-only *event* lines, keyed by
   `{repo, pr}` — not one-line-per-landed-PR, which would silently drop
   the closed, parked, stuck-open, and abandoned PRs whose reviews spent
   credits all the same (the expensive tail the loss analysis most needs):
   - a `review_cycle` event as each cycle completes:
     `{ts, event:"review_cycle", repo, pr, head_sha, tier, tier_source,
     cycle, reviewers_requested[], findings_per_bot: {total, unique,
     critical}, claude_cost_proxy}`;
   - a `terminal` event when the PR merges or closes:
     `{ts, event:"terminal", repo, pr, tier, cycles_used, merged,
     fixes_pr?}`. A PR still open at analysis time simply has no terminal
     event — visible, not missing.
   - `claude_cost_proxy`: a token proxy per claude review (diff bytes in +
     review bytes out) — the number that answers "are reviews a material
     fraction of the pool, or are driver runs the real drain".
   - `unique` findings attribution reuses the review-coordinator verdict /
     review-findings grouping — recorded, not re-judged.
   - **Escaped-defect linkage:** when a PR declares it fixes a prior PR,
     record `fixes_pr` so the original PR's tier + reviewer set can be
     joined later. This is the only signal that ever catches "the cut
     reviewer would have found it".
   - Session-engine parity: in `--engine session` runs the skill appends the
     same records itself (ship's land hook never fires there).
   - Best-effort append — a write failure warns, never blocks a land.
4. **Known coverage gap, stated:** hand-opened PRs outside the driver get no
   spend record until the recipe itself runs `triage-floor` at PR-open —
   that wiring rides with Phase 1's recipe change. Driver + session runs
   cover the large majority of PRs; the gap is logged, not hidden.

**Rollback/decision triggers (defined now, before any cut):**

- If after ~30 days claude reviews come out < ~20% of Max-pool spend,
  Phase 1's @claude cut is not worth its risk — retarget driver runs.
  **Denominator:** the proxy alone can't decide this — it has no pool
  total. At re-eval the operator pulls the Claude usage view for the same
  window (the whole Max pool: interactive + driver runs + reviews); the
  numerator is the calibrated proxy sum (bytes/4 ≈ tokens). Coarse is
  fine — the trigger asks "is this share material", not for a decimal.
  If a finer split is wanted, driver-run transcripts already exist in
  ship's store to proxy the driver share the same way.
- If escaped-defect linkage ever shows a fixed defect whose original PR was
  T0/T1, that tier's reviewer set gets *stronger*, not weaker, until the
  holdout says otherwise.

## Phase 1 — tier-routed reviewer sets (PERSONAL CANARY; expansion gated)

The first implemented canary mapping is configurable and content-addressed,
not permanent truth:

- **T0:** one local adversarial pass, no cloud bots, cap 1. Uncertainty
  reclassifies upward instead of granting another T0 cycle.
- **T1:** Codex, cap 3.
- **T2:** configured panel excluding Claude, coordinator required, cap 3.
- **T3:** full four-bot panel plus adversarial verification, cap 8. Cycles
  4–8 require a finding/proof-specific rationale.
- **Later cycles:** request only missing required reviewers and authors whose
  findings remain in play. Caps are ceilings, not quotas.
- **Closure:** deterministic proof may replace noncritical T0–T2 rereview;
  noncritical findings may be explicitly deferred. Critical findings, failed
  proofs, and missing required evidence cannot be deferred.
- **Cursor mention-only follow-through:** if Bugbot flips to mention-only
  (operator dashboard act), the T2/T3 recipes must add the `@cursor review`
  mention explicitly or the "full panel" quietly becomes 3 bots.

**Expansion conditions:** all four live canary cases are complete at their
exact heads; implementation PR CI and configured reviews are green; Ship and
session adapters remain equivalent; stale-head replay stays impossible; and
targeted-cycle/deferment evidence is operator-reviewed. The exponential
`1,2,4,8,...` continuation weight remains shadow telemetry until evidence
supports an enforcement threshold.

## Open operator calls (not agent decisions)

1. **Fund a shadow holdout before wider expansion?** It spends review credit
   on purpose to price the cut's risk. Without it the re-eval can only see what
   the remaining bots found.
2. **Cursor Bugbot mention-only toggle** — account-level dashboard setting,
   and its timing relative to Phase 1.

## Next steps

1. ~~Sweep + baseline~~ done (tables above).
2. ~~Shape decision~~ 2026-07-22: measurement lives in THIS doc as Phase 0 —
   no separate TDD (it would restate this context and drift), not a triage
   sub-spec (the spend log and holdout are panel policy, out of triage's
   scope; only the path-override table lands in triage).
3. Land the Workbench policy/session adapters, Ship adapter, and canonical
   work-driver orchestration only after exact-head review and Gate.
4. Complete the personal canary evidence, then evaluate the expansion
   conditions above.
5. Operator calls on holdout funding and the Cursor toggle decide any rollout
   beyond the personal canary.
