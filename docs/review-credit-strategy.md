# Review-credit strategy — working state (2026-07-20)

Status: proposal — under review. Data sweep complete. Direction validated;
policy pending sign-off after addressing review (see PR comments).

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
- Instrumentation seams that already exist: `labels/mismatches.jsonl`
  (per-/pr-risk-run log), review-coordinator JSON verdict, `revtriage.exe
  -json` (local digest). Passive today.

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

**Headline: 71% of PRs (T0+T1) got the same review blast as the critical 5%.**
Bot activity barely differentiates by risk today (1.8 vs 3.5 avg reviews) —
spend is driven by PR count, exactly what the triage spec predicted.
Encouraging validation: bots naturally comment ~2.4x more on T3 than T1, so
the floor's tiers correlate with where findings actually concentrate.

**Per-bot activity (review submissions + comments, top 5 repos = 261 PRs):**

| Bot | events | notes |
|---|---|---|
| claude[bot] | 397 | ~1.5/PR — **most active bot, 100% billed to the Max pool** |
| codex | 393 | ChatGPT sub |
| cursor[bot] | 362 | Cursor credits, auto-fires on every PR |
| copilot | 142 | Copilot sub, cheapest |

claude[bot] being the single most active reviewer confirms the diagnosis:
review blast is the Max-pool drain. Restricting @claude to T2/T3 (29% of
PRs) cuts its review invocations ~70% with zero loss on the PRs where
findings actually concentrate.

## Strategy direction (draft — pending sweep numbers)

Route review spend by tier instead of flat-blasting. Sketch:

- **T0 (tests/docs/generated):** no cloud bots. Local `/review-digest`-class
  pass at most. Merge on CI green + gate.
- **T1 (standard):** ONE cloud reviewer (cheapest adequate — likely codex or
  copilot, since those subs are underused vs the Max pool), 1 cycle default;
  escalate only if it flags something critical.
- **T2 (sensitive):** current panel minus @claude, or panel with 1 cycle +
  coordinator quorum; @claude only when the coordinator verdict is unclear.
- **T3 (critical/gate-machinery):** full 4-bot panel + 3 cycles + adversarial
  pass (per feedback_adversarial_workflow_for_gates). Spend is fine here.
- **Cursor Bugbot:** decide per data — if its auto-fire on T0/T1 PRs produces
  few unique findings, flip to mention-only in the Cursor dashboard
  (account-level setting; operator action, not agent).
- **Cycles:** cap at 1 by default; the 3-cycle cap becomes T2+/on-findings
  only. Merge-early-on-ship-it already policy.
- **Instrumentation to keep:** wire `/work-driver` to run `triage-floor` at
  PR-open and append tier + reviewer-set + cycles-used + unique-findings-by-
  bot to a jsonl (extend `labels/mismatches.jsonl` or new
  `review-spend.jsonl`). After ~a month, re-evaluate which bots earn their
  slot per tier (the spec's "advisory must earn its slot" discipline, applied
  to reviewers).

Implementation would be: update ship/CLAUDE.md recipe + /work-driver policy
(prose), wire driver→pr-risk shell-out, add the spend log. Design-doc-first
per feedback_design_doc_then_pr if it grows beyond prose edits.

## Next steps on resume

1. ~~Sweep + baseline~~ done (tables above). 2. ~~Operator sign-off~~ approved
2026-07-22 (operator handles the Cursor Bugbot dashboard toggle directly).
3. ~~Seed~~ SEEDED 2026-07-22: dossier ship project, phase
`review-credit-tiering`, 4 tasks (review-recipe-tiered → driver-triage-tier →
review-spend-log; work-driver-skill-tier-policy after the recipe).

Next: `/work-driver-prep project:ship:phase:review-credit-tiering`, then
`/work-driver`. After ~30 days of review-spend.jsonl data, re-evaluate which
bots earn their slot per tier.
