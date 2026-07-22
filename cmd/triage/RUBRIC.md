# RUBRIC.md — the PR risk classifier

This file **is** the classifier. The deterministic floor below is applied by simple path/content/filename matching — no agent needed. An agent advisory pass may only *escalate* above the floor (spec §6). Changing risk policy = a reviewed edit here.

**This file is control plane: editing it is a T3 change (see §5.4).** Every classification records this file's git SHA so results compare only within a version. Design rationale: `docs/features/pr-risk-engine/spec.md`.

## Tiers & routing

| Tier | Name | Routing (human requirement) |
|---|---|---|
| T0 | auto | No human. Gates green → auto-merge eligible (narrow safe slice only, see below). |
| T1 | standard | One peer review. |
| T2 | sensitive | Owner (CODEOWNERS) review required. |
| T3 | critical | Owner + adversarial skeptic pass + author "why this is safe" defense. |

## How a tier is computed

```
floor = max(floor(s) for s in ALL deterministic signals that fire)   # reproducible, instant
final = max(floor, agent_escalation)   # agent may only RAISE above floor, never lower (advisory, logged)
```

Fail-closed (spec §7): agent failure/timeout → keep floor, BUT never trust a T0/T1 floor for a dependency/policy/content-sensitive diff → **T2**. Missing/malformed rubric → **T2**. Unknown path → **T1**, never T0.

## §5.4 Control-plane signals — HIGHEST PRIORITY, floor T3

Any diff touching the risk system's own control plane floors at **T3** and is **exempt from the docs/generated → T0 row**:
`RUBRIC.md` · the `/pr-risk` skill · the routing table · `labels/**` · `mismatches.jsonl` · `CODEOWNERS` · branch-protection config · any CI that invokes triage.
*A change to the classifier or its evidence is the highest-risk class — one silent auto-merge lowers the floor for all future PRs.*

## §5.1 Surface signals (path/keyword)

| Touches… | Floor |
|---|---|
| DB migration: destructive / backfill / edit to an already-applied migration / unrecognized statement | T3 |
| DB migration: purely additive **new** migration file (CREATE TABLE / CREATE INDEX / ADD COLUMN / index swap) | T2 |
| auth / authz / session / crypto / secrets / token | T3 |
| money / billing / ledger / payment / invoice | T3 |
| irreversible op: delete path, data destruction, non-reversible migration | T3 |
| public API / exported type / wire contract | T2 |
| persisted data shape (stored/serialized format) | T2 |
| infra/deploy config: CI, Dockerfile, IaC, feature-flag defaults | T2 |
| concurrency / locking / retry / idempotency / ordering | T2 |
| internal (non-exported) behavior change | T1 |
| pure refactor / comments / tests-only / generated / copy / **non-policy** docs | T0 |

## §5.2 Content signals (diff-text, path-independent)

Fire on line content regardless of filename — closes the "danger in an unnamed file" hole:

| When the diff… | Floor |
|---|---|
| **removes** a line matching authz/permission/validation/rate-limit/crypto call patterns | T3 |
| loosens a guard comparison (`==`→`!=`, `&&`→`\|\|` in a check) or deletes an assert guarding a sensitive path | T2 |
| introduces unbounded/uncapped construct (unpaginated query, uncapped retry/spend, unbounded loop over external input) | T2 |

## §5.3 Dependency / supply-chain (lockfiles are NOT "generated → T0")

| Touches… | Floor |
|---|---|
| new/bumped **runtime** dependency (ships in the built artifact) | T2 |
| new/bumped **dev/test-only** dependency (`[dev-dependencies]`, `devDependencies`, test-only crates like `proptest`) | T1 |
| manifest (`package.json`, `Cargo.toml`, `go.mod`, `pyproject.toml`, `mix.exs`) — runtime section | T2 |
| lockfile version bump of a **runtime** dep (`*-lock.json`, `Cargo.lock`, `go.sum`, `poetry.lock`) | T2 |
| lockfile churn caused by a **dev-only** manifest change in the same diff | T1 |
| registry/source override: `.npmrc`, `[patch]`, `resolutions`, `overrides`, `git`/`path` dep | T3 |

*Rationale (Experiment 01): a dev/test dependency add — e.g. `proptest` on a tests-only PR — must not drag the PR to owner-review. Runtime-vs-dev is the discriminator; a runtime dep can execute install/build scripts and ship in the artifact, a dev dep can't reach production. Held-out gate (HELDOUT-01): the section a dep lands in — visible only as diff context — marks it dev, and the lockfile churn of a dev-only change inherits dev; a lockfile-only diff stays runtime, fail-closed. Additive-migration split (HELDOUT-01): blind consensus rated all three additive held-out migrations owner-review, and T2 still routes to a human — destructive, unrecognized, or edited-in-place migrations keep T3.*

## §5.5 Policy-as-data

| File/content | Floor |
|---|---|
| IAM / rego / k8s RBAC / RoleBinding / Secret manifests | T3 |
| CORS / allowed-origins / security toggles (`require_2fa`, etc.) in config data | T2 |
| `.env*`, secret-bearing config | T2 |
| unknown path, no signal matched | T1 |

## §5.6 Size signals (weakest)

LOC/file-count nudges within T0↔T1 only. Size never sets T2/T3 and never alone lifts a well-tested pure change out of T0. A 3-line auth change outranks a 2,000-line tested codegen regeneration.

## §5.7 Per-repo path overrides (the deterministic gate-machinery floor)

§5.4 covers only triage's *own* control plane; another repo's gate/driver/merge machinery reads as "internal → T1" from its path alone (HELDOUT-01: 8 of 15 under-calls were exactly this). Under blast-everything the 4-bot panel was the compensating control; any policy routing review on the floor alone inherits the blind spot. This section makes that compensating control **deterministic** for the repos whose layout we own.

A compiled-in table maps `repo → [path glob → minimum tier]`, applied per file as **`max(floor, override)`** — overrides only ever **raise**, never lower. Repo identity comes from a `-repo owner/name` flag (driver, recipes, and gate's ladder all pass it); **absent `-repo` ⇒ overrides skipped, behavior byte-identical to the pre-override floor**, and no repo's globs ever apply to another. The table encodes OUR repo layout, so changing it is a reviewed PR — exactly the bar for a classifier control-plane change. Every override hit is its own `path-override` finding (band label + file path), so a `-v` verdict stays explainable. Two bands, split by consequence:

| Band | Paths | Floor |
|---|---|---|
| Merge-authorization + the exit-code seam | workbench `cmd/gate/internal/state/**`, `cmd/gate/internal/verify/**`, `cmd/gate/main.go` (owns the 0/1/2/3/4 exit-code contract) | **T3** |
| Broader gate / driver / triage machinery | workbench rest of `cmd/gate/**` (e.g. `internal/evidence/**`, `internal/observe/**`), `cmd/triage/**`; ship `packages/driver/**` | **T2** |

*Why T3 for the first band: a fail-open in merge authorization or the exit-code contract drops @claude and the adversarial pass exactly where it matters most — HELDOUT-01's own blind labels put gate#3/#5/#9 at T3, and a T2 there would not. The bands need no mutual exclusion: a file matching both (e.g. `cmd/gate/main.go` matches the exit-code rule and the broad gate rule) resolves to the higher tier by max. Existing path rules are untouched — `labels/**` still floors T3 and wins by max.*

## Agent advisory pass (spec §6)

After the floor, an agent may propose an escalation for the *semantic residual* the deterministic signals can't express. Constraints: escalate-only, logged `{floor, proposed, why}`. Its dogfood job is to prove it fires usefully above the floor — if it never does, v0 ships deterministic-only.

Known escalation triggers (grow this list as the dogfood surfaces them; graduate each into a deterministic signal when its pattern stabilizes). An escalating proposal must name one and quote verbatim evidence from the diff — `internal/advisory` verifies both and a rejected proposal contributes nothing:
- `trust-boundary-widening` — a logic change widening a trust boundary without matching a content pattern (sandbox-disable, CI checking out untrusted PR-head, network/VM isolation edits, secrets plumbing below the keyword threshold) → propose T2/T3;
- `production-default` — a default whose production impact needs understanding, not pattern-matching → propose T2;
- `invariant-relocation` — **a refactor that relocates policy / invariant / state-machine-bearing code, or a change to what such code enforces** (even if behavior-preserving, even if it looks mechanical) → propose **T2**. *(Experiment 01: dossier#67 moved the task state-machine guards to a new module; the floor read "internal change → T1" while both blind labelers wanted owner review. Mechanical-looking ≠ low-risk when the code owns invariants.)*
- `gate-machinery` — code that decides what merges, ships, or passes verification in ANY repo (merge gates, verifiers, review-cycle enforcement, preflight/doctor gates, escape-detection predicates — a bug fails open) → propose T2/T3. *(HELDOUT-01: 8 of the 15 held-out under-calls. §5.4 covers only triage's own control plane. For the repos whose layout we own, §5.7's per-repo path overrides now floor this deterministically when `-repo` is passed; this trigger remains the compensating control for OTHER repos and for machinery outside the compiled-in globs.)*
- `plan-of-record` — a design doc that SETS policy (merge authority, trust ladders, escalation rules) rather than describing code → propose T2. *(HELDOUT-01: ship#172/#182 — "non-policy docs → T0" mis-prices a doc that is itself the policy.)*

## Auto-merge safe slice (v0)

Only these deterministically-detected classes auto-merge in v0: **tests-only · generated code · non-policy docs**. Control-plane (§5.4) is excluded even though it's "docs/config."

## Output contract

Emit `final_tier`, `floor_tier`, `floor_signals[]`, `agent_tier`, `agent_escalation`, `route`, `rubric_sha`. Append the record to `labels/mismatches.jsonl`.
