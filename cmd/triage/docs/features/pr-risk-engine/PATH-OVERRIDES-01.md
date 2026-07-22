# PATH-OVERRIDES-01 — per-repo path overrides (2026-07-22)

## Why

HELDOUT-01 found 15 held-out under-calls; **8 were gate machinery** — merge
gates, verifiers, and driver paths the floor reads as "internal → T1" from the
path alone. §5.4 deliberately floors only triage's *own* control plane, so
another repo's gate code stayed semantic residual owed to the advisory pass.
Under blast-everything the 4-bot panel was the compensating control. Any policy
that routes review on the floor alone (Phase 0.2 of `review-credit-strategy`)
inherits that blind spot. This makes the compensating control **deterministic**
for the repos whose layout we own.

## What

A compiled-in table (`internal/floor/overrides.go`) maps
`repo → [path glob → minimum tier]`, applied per file as **`max(floor,
override)`**. It is rubric-shaped policy living in the rubric layer; the
classifier core in `floor.go` only applies it. See RUBRIC.md §5.7.

- **Repo identity via `-repo owner/name`.** Callers — driver, recipes, and
  gate's ladder shell-out (`cmd/gate/internal/verify/floor.go`) — all know the
  PR's repo and pass it. **Absent `-repo` ⇒ overrides skipped, byte-identical to
  the pre-override floor.** No repo's globs ever apply to another repo's diff.
- **Raise-only.** `max(floor, override)` — a floor T3 stays T3; an override
  never lowers a tier.
- **Compiled-in, not a config file.** The table encodes OUR repo layout;
  changing it is a reviewed PR — exactly the bar for a classifier control-plane
  change. A config-file mechanism for hypothetical other users is a non-goal.
- **Explainable.** Each override hit is its own `path-override` finding (band
  label + file path), so a `-v` verdict names why a tier was raised.

## The two bands

Split by consequence:

| Band | Paths (repo) | Floor |
|---|---|---|
| Merge-authorization + the exit-code seam | workbench `cmd/gate/internal/state/**`, `cmd/gate/internal/verify/**`, `cmd/gate/main.go` | **T3** |
| Broader gate / driver / triage machinery | workbench rest of `cmd/gate/**` (`internal/evidence/**`, `internal/observe/**`, …), `cmd/triage/**`; ship `packages/driver/**` | **T2** |

The T3 band is the merge-authorization state, the verifier ladder, and the
load-bearing 0/1/2/3/4 exit-code contract (`cmd/gate/main.go`) — a fail-open
here drops @claude and the adversarial pass exactly where it matters most.
HELDOUT-01's own blind labels put gate#3/#5/#9 at T3; a T2 there would not.

The bands need no mutual exclusion — a file matching both (e.g.
`cmd/gate/main.go` matches the exit-code rule and the broad `cmd/gate/**` rule)
resolves to the higher tier by max. Existing RUBRIC path rules are untouched:
`labels/**` already floors T3 and wins by max.

## Acceptance (pinned by `internal/floor/overrides_test.go`)

With `-repo itsHabib/workbench`:

- a diff touching only `cmd/gate/internal/state/anchor.go` **or** only
  `cmd/gate/main.go` classifies **T3**;
- a diff touching only `cmd/gate/internal/evidence/evidence.go` classifies
  **T2**.

Without `-repo`, behavior is byte-identical to the pre-override floor
(`ClassifyRepo(d, "")` deep-equals `Classify(d)`; an unknown repo is inert). A
floor T3 stays T3 with overrides present. `-v` names each override hit. The
overrides need `-repo`, and the eval corpus (`labels/score-floor.mjs`) does not
pass it, so corpus/eval scoring is unchanged.

## Non-goals

Reviewer-set routing (parked Phase 1), `triage-advisory`, any `labels/**`
edits, and config-file mechanisms for hypothetical other users.
