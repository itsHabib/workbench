**Status**: draft
**Owner**: @michael
**Date**: 2026-07-22
**Related**: dossier task `triage-path-overrides` (id: `tsk_01KY5CNX02N5SPYE130PTBJHX1`, ship project, phase `review-credit-tiering`), `docs/review-credit-strategy.md` Phase 0.2

# Per-repo path overrides — gate/driver/merge machinery floors deterministically — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `cmd/triage/internal/floor/overrides.go` (new), floor integration point, `cmd/triage/triage-floor/main.go` (the `-repo` flag), `cmd/gate/internal/verify/floor.go` (gate's shell-out passes `-repo`) | ~140 | 140 |
| Tests | `overrides_test.go` (new), floor suite additions | ~180 | 90 |
| Docs | `cmd/triage/docs/` + RUBRIC note | ~30 | 15 |
| **Total** | | | **~225** |

Band: **ideal** per repo's PR sizing convention.

## Goal

triage-floor's documented blind spot is gate machinery: HELDOUT-01 found 15 under-calls, 8 of them gate-machinery — merge gates, verifiers, driver paths the floor reads as "internal → T1". Under blast-everything the 4-bot panel was the compensating control; any policy that routes review on the floor alone inherits the blind spot. Make the compensating control deterministic.

## Behavior / fix

Add a per-repo path-override table to triage-floor: a rubric-shaped, compiled-in map of `repo → [path glob → minimum tier]`, applied as `max(floor tier, override tier)` per file. **Two bands, split by consequence:**

- **T3 band — merge-authorization and signing paths**: workbench `cmd/gate/internal/state/**`, `cmd/gate/internal/verify/**`, and `cmd/gate/main.go` (it owns the 0/1/2/3/4 exit-code contract — a load-bearing seam per the repo CLAUDE.md, squarely "exit-code machinery"). HELDOUT-01's own labels put gate#3/#5/#9 at T3; under the parked Phase 1 mapping, T2 would still drop @claude and the adversarial pass exactly where a fail-open matters most.
- **T2 band — broader gate/driver machinery**: ship `packages/driver/**`, the rest of workbench `cmd/gate/**` (e.g. `internal/evidence/**`, `internal/observe/**`), `cmd/triage/**`.
- Existing RUBRIC rules stay untouched (`labels/**` → T3 already holds).

Mechanics:

- Repo identity via a new `-repo owner/name` flag (callers — driver, recipes, gate — all know it). Absent flag ⇒ overrides skipped, behavior byte-identical to today; nothing global ever applies another repo's globs.
- Overrides only raise, never lower — a floor T3 stays T3.
- Output shape unchanged (tier line + `-v` findings); an override hit shows in `-v` as its own finding line (e.g. `path-override: gate machinery: cmd/gate/internal/state/anchor.go`) so verdicts stay explainable.
- Compiled-in table, not a config file — it encodes OUR repo layout; changing it is a reviewed PR, which is exactly right for classifier control-plane changes.
- House style: table in its own file, policy stays in the rubric layer, ≤2 nesting per scope.

## Acceptance

With `-repo itsHabib/workbench`: a diff touching only `cmd/gate/internal/state/anchor.go` or only `cmd/gate/main.go` classifies **T3**; a diff touching only `cmd/gate/internal/evidence/evidence.go` classifies **T2**. Without `-repo`, behavior is byte-identical to today. A floor T3 stays T3 with overrides present. `-v` names each override hit. Gate's own shell-out to triage-floor (`cmd/gate/internal/verify/floor.go`) passes `-repo`. RUBRIC/docs state the two-band rule.

## Test plan

Unit tests: raise-only semantics; no-flag passthrough; glob matching per repo; band split (state/verify → T3, other gate paths → T2); `-v` finding line; T3 preserved. Existing floor suite green; corpus/eval gate unchanged (overrides need `-repo`, the eval corpus doesn't pass it).

## Non-goals

Reviewer-set routing (parked Phase 1), `triage-advisory`, any `labels/**` edits, config-file mechanisms for hypothetical other users.
