# Review-credit strategy — measure first, cut second (v2, 2026-07-22)

Status: proposal v2 — restructured after design review (PR #90). The v1 draft
routed reviewer sets by tier immediately; review found the cut was aimed by
event counts, not cost, and removed the compensating control for the floor's
known blind spot. v2 splits the work: **Phase 0 builds the measurement and the
deterministic guardrail now (no-regret); Phase 1 — the actual reviewer cut —
is parked behind that data.**

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

1. **Driver carries the tier.** Ship's driver classifies each stream's PR via
   `triage-floor` at PR-observe time, fail-closed to T2 (missing binary,
   exit 1, garbage output ⇒ T2 + warning, never T0/T1). Tier persists on the
   stream and shows in status/render. Mechanism only.
2. **Per-repo path overrides lift gate/driver/merge machinery to T2,
   deterministically.** One rubric-shaped table in triage (e.g. ship's
   `packages/driver/**`, workbench `cmd/gate/**`, merge/verify paths) —
   floor + overrides, not floor alone. This is the compensating control for
   HELDOUT-01's gate-machinery under-calls, cheap and testable now, useful
   regardless of Phase 1's fate.
3. **`review-spend.jsonl` — a fresh file** (ship state dir, same convention
   as the store; never `labels/**`). One line per landed PR:
   `{ts, repo, pr, head_sha, tier, reviewers_requested[], cycles_used,
   findings_per_bot: {total, unique, critical}, claude_cost_proxy, fixes_pr?,
   merged}`.
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
     same record itself (ship's land hook never fires there).
   - Best-effort append — a write failure warns, never blocks a land.
4. **Known coverage gap, stated:** hand-opened PRs outside the driver get no
   spend record until the recipe itself runs `triage-floor` at PR-open —
   that wiring rides with Phase 1's recipe change. Driver + session runs
   cover the large majority of PRs; the gap is logged, not hidden.

**Rollback/decision triggers (defined now, before any cut):**

- If after ~30 days the cost proxy shows claude reviews < ~20% of Max-pool
  spend, Phase 1's @claude cut is not worth its risk — retarget driver runs.
- If escaped-defect linkage ever shows a fixed defect whose original PR was
  T0/T1, that tier's reviewer set gets *stronger*, not weaker, until the
  holdout says otherwise.

## Phase 1 — tier-routed reviewer sets (PARKED, data-gated)

The v1 mapping, kept for when the data clears it (dossier tasks
`review-recipe-tiered` + `work-driver-skill-tier-policy` are seeded and
blocked on this gate):

- **T0:** no cloud bots — contingent on promoting the local
  `/review-digest`-class pass to *count as the review* (else it collides
  with the no-self-merge rule), and on RUBRIC's narrow T0 slice, not the
  floor's broader one.
- **T1:** one cloud reviewer, 1 cycle — default **codex**; escalate on any
  critical finding.
- **T2:** panel minus @claude + coordinator quorum; @claude summoned on
  murky verdicts.
- **T3:** full 4-bot panel + 3 cycles + adversarial pass
  (feedback_adversarial_workflow_for_gates). Spend is fine here.
- **Cursor mention-only follow-through:** if Bugbot flips to mention-only
  (operator dashboard act), the T2/T3 recipes must add the `@cursor review`
  mention explicitly or the "full panel" quietly becomes 3 bots.
- **Cycles:** cap 1 by default; 3 becomes T2+/on-findings only.

**Unpark conditions (all of them):** 30 days of `review-spend.jsonl`; cost
proxy shows reviews are a material pool fraction (else retarget); the
gate-machinery path overrides are live; and either a shadow holdout (full
panel on a random 10–15% of T0/T1 for the first month, compared against the
routed set) is funded, or every "zero loss" claim stays dead and the cut is
sold as "loss unknown, instrumented".

## Open operator calls (not agent decisions)

1. **Fund the shadow holdout?** It spends review credit on purpose to price
   the cut's risk. Without it the re-eval can only see what the remaining
   bots found.
2. **Cursor Bugbot mention-only toggle** — account-level dashboard setting,
   and its timing relative to Phase 1.

## Next steps

1. ~~Sweep + baseline~~ done (tables above).
2. ~~Shape decision~~ 2026-07-22: measurement lives in THIS doc as Phase 0 —
   no separate TDD (it would restate this context and drift), not a triage
   sub-spec (the spend log and holdout are panel policy, out of triage's
   scope; only the path-override table lands in triage).
3. Dossier realigned to v2: `driver-triage-tier`, `review-spend-log` (token
   proxy + fixes_pr folded in), `triage-path-overrides` are the Phase 0
   batch; `review-recipe-tiered` + `work-driver-skill-tier-policy` blocked
   behind the unpark conditions.
4. `/work-driver-prep project:ship:phase:review-credit-tiering` → `/work-driver`
   for the Phase 0 batch.
5. ~30 days after the spend log lands: re-evaluate against the triggers
   above; operator calls on holdout + cursor toggle decide Phase 1.
