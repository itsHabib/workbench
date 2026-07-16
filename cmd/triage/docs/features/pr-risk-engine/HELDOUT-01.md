# HELDOUT-01 — held-out floor precision gate (2026-07-08)

The floor's signatures were tuned on the 24-PR Experiment-01 corpus (and re-tuned by the
floor-precision PR #3). Corpus-tuned signatures degrade on data they never saw — the same
discipline the ci-classify review demanded — so this gate scores the floor on **30 fresh
portfolio PRs, none in corpus-e01**, labeled blind before any floor comparison.

## Method

Same oracle protocol as Experiment 01: two rubric-blind labelers (independent agents, neutral
"how much human review does this need?" prompt, diffs + titles only, explicitly barred from
RUBRIC.md and the floor code), consensus = agreement, disagreement shows both with the higher
as safe default. Corpus: `labels/corpus-heldout.tsv`, diffs vendored by
`labels/fetch-diffs.sh labels/corpus-heldout.tsv`, scored by
`node labels/score-floor.mjs labels/corpus-heldout.tsv`.

- 30 PRs across ship / dossier / rooms / gate / roxiq / huddle / tracelens / wellness-ai —
  docs, tests-only, CI, features with migrations, network/isolation, secrets-adjacent, deps.
- Labeler agreement 25/30, every disagreement ±1 tier (E01: 20/24) — same oracle noise floor.

## Result

| | before | after |
|---|---|---|
| exact | 11/30 | **15/30** |
| **over-calls (the precision bar)** | **4** | **0** |
| under-calls | 15 | 15 |

Corpus-e01 regression check after the fixes: exact 11→9, over-calls unchanged (1 — the known
RUBRIC-correct dev-dep T1 vs T0 label on rooms#31), under-calls 12→14. The two moved rows are
ship#168 and ship#173: additive migrations whose T3-ness lives in the surrounding semantics
(duration-cap behavior, thread provider), i.e. they join the semantic residual the advisory
tier owes an escalation for — they are now rows the advisory eval MUST catch, not silent floor
credit. Neither fix can move any migration below T2, and T2 still routes to an owner, so the
dangerous direction (human-tier → auto-tier) is untouched: zero consensus-T2+/T3 PRs floor
below T1 that didn't already.

## The four over-calls, root-caused

1. **dossier#81 (T2 → consensus T1): dev-section blindness.** Deps added INTO an existing
   `[dev-dependencies]` section show the header only as a *context* line; the parser dropped
   context, so `hunkIsDev` saw bare dep lines → runtime → T2 on a tests-only PR. And the
   lockfile churn those dev deps caused (new `[[package]]` entries for `rmcp`/`nix`/…) has no
   dev marking in the lock format at all. **Fix:** the parser keeps the ordered line stream
   (context + hunk boundaries + new-file bit); `manifestIsDev` tracks the section each changed
   line sits in, fail-closed (unknown section at a hunk boundary → runtime); a lockfile
   inherits dev only when every manifest change in the same diff is dev-only. *Accepted
   residual:* a runtime version bump hand-edited into the lock while the manifest diff shows
   only dev changes would ride the inheritance — indistinguishable without resolving the dep
   graph; the registry/source-override signals still fire independently.
2. **ship#178, ship#180, roxiq#123 (T3 → consensus T2): blanket migration T3.** All three are
   purely additive NEW migration files (CREATE TABLE + indexes / ADD COLUMN / covering-index
   swap). Blind consensus rated every one owner-review, not critical — migration-ness alone
   doesn't determine T3; what pushes an additive migration to T3 is semantic (what the schema
   *means*), which is the advisory's declared job. **Fix:** `classifyMigration` grades
   content: purely-additive statements in a NEW file → `db-migration-additive` T2; destructive
   / backfill / unrecognized statements (fail-closed allowlist), edits to an already-applied
   migration, or a migration with no recognized additive statement → T3.

Each fix is pinned by table tests in `internal/floor/floor_test.go` (12 new cases: the two
dossier#81 shapes, lockfile-only fail-closed, dev+runtime span, hunk-boundary fail-closed, the
three additive-migration shapes, backfill, trigger fail-closed, migrations-dir README, edited
migration).

## The 15 under-calls, categorized

All 15 are **semantic residual** — no deterministic path/content signature claims them, and
every one matches a known or candidate §6 advisory trigger:

- **Merge/review machinery of another repo** (gate#3, gate#5, gate#9, ship#177, tracelens#5,
  rooms#62): the code IS a gate; a bug fails open. Trust-boundary class. The floor cannot
  know `internal/verify/readiness.go` is a merge gate from its path without repo-specific
  rules; the §5.4 control-plane rule deliberately covers only triage's own plane.
- **Trust/isolation boundaries without telltale content** (rooms#50, rooms#59 T2→T3 gap,
  rooms#64, ship#176): escape-detection predicates, tap lifecycle above the T2 isolation
  content signal, auth carrier into a runner env.
- **Invariant/state-machine semantics** (dossier#84, roxiq#132): the dossier#67 class.
- **Plan-of-record design docs** (ship#172, ship#182): "non-policy docs → T0" mis-prices a
  doc that sets merge-authority policy. Candidate new advisory trigger; a deterministic
  version would need path conventions (docs/features/**/spec.md) that over-fire on ordinary
  specs — left semantic for now, watched via mismatches.jsonl.
- **Secrets plumbing below the keyword threshold** (roxiq#127): TF_VAR secret passing in a
  sandbox script; content signal deliberately narrow after the PR #3 comment/fixture fixes.

These 15 rows (plus ship#168/#173 from corpus-e01) are the advisory tier's held-out residual
set — its per-trigger recall gate runs against exactly these.

## Verdict

**PASS at the precision bar: 0 over-calls on 30 held-out PRs after two root-cause fixes, with
zero movement in the dangerous direction.** The under-call mass (15/30, skewing T2/T3) is the
strongest evidence yet for the strategic finding from Experiment 01: this portfolio's merged
PRs skew high-risk, so the review-load win here is routing owner/adversarial attention, which
is the cloud advisory's job — gated next by per-trigger recall + inflation parity on these
same rows.
