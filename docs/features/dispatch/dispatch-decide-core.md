**Status**: draft
**Owner**: @itsHabib
**Date**: 2026-07-15
**Related**: dossier task `dispatch-decide-core` (id: `tsk_01KXKC4YEPR094Z1SG05AQTC9K`), design [docs/features/dispatch/spec.md](spec.md) (the merged TDD — the binding contract; this spec is the phase-1 build order over it)

# dispatch decide core — design spec

Phase 1 of the dispatch TDD: the `decide` + `validate` verbs end-to-end. The TDD
is the contract; read §5 (data model), §6 (API), §7 (flows), §8 (concurrency)
before writing code — this doc is the deliverable list and the acceptance bar,
not a re-statement of the design.

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `cmd/dispatch/main.go` (flags, dispatch, exit codes) · `cmd/dispatch/internal/policy/policy.go` (load, validate, sha256, task_class enum) · `cmd/dispatch/internal/placement/{placement.go,match.go}` (first-match, schema_version, provenance) · `cmd/dispatch/internal/receipt/receipt.go` (append, exit-5 ordering) | ~450 | ~450 |
| Tests | `*_test.go` beside each package — table tests over the §7.2 failure paths, golden placements, validate-lint cases, receipt-presence assertions | ~300 | ~150 |
| Config/docs | `cmd/dispatch/CLAUDE.md` + `cmd/dispatch/docs/DESIGN.md` (per workbench per-tool convention), this spec | — | 0 |
| **Total** | | | **~600** |

Band: **ideal** (<700) per workbench PR sizing. No `drift` verb, no scorecard, no
integration — those are later phases.

## Goal

Stand up `cmd/dispatch` as a workbench tenant (per-tool `internal/` tree, stdlib
only) whose `decide` verb turns a task descriptor + a versioned policy file into a
deterministic placement + a receipt, and whose `validate` verb pre-flights a
policy for authors. The exit-code contract is the load-bearing surface — every
downstream consumer keys on it — so correctness of the failure paths matters more
than the happy path.

## Behavior / fix

Build exactly the TDD's phase-1 row. Concretely:

1. **`dispatch decide --policy <path> [--task <json> | stdin] [--receipts <path>]`** — TDD §7.1:
   - Load policy → validate schema → compute sha256. Invalid/missing/empty
     (`rules: []`) or an unknown `task_class` in a `match` block → **exit 2**,
     before the descriptor is read.
   - Parse + validate the descriptor (`{repo, task_class, weighted_loc,
     risk_tier, budget?}`). Bad descriptor → **exit 4**.
   - First-match scan of `rules` in file order. No match → **exit 3**, stderr
     carries the actual unmatched *values* (`task_class=analytical risk_tier=T2`),
     never a fallback placement.
   - If `--receipts`: append the receipt **before** emitting the placement
     (TDD §7.1 step 4 / §7.2 — v2.1 ordering); an append failure → **exit 5**
     with nothing on stdout, preserving "no placement on a non-zero exit."
   - Emit placement JSON to stdout (`schema_version`, `place` fields,
     `escalation`, `{rule, policy_version, policy_sha256}`) → **exit 0**.

2. **`dispatch validate --policy <path>`** — author pre-flight, same loader as
   decide (~zero marginal cost): schema, hash, `task_class` enum, and a catch-all
   lint (warn if the policy has no `match: {}` rule). Exit 0 valid / 1
   valid-with-warnings / 2 invalid.

3. **Taxonomy + derivation are frozen here** (TDD §5 — the phase-2 replay gate is
   circular without them): `task_class` is an enumerated `mechanical | analytical
   | generative` (complexity only; size lives in `weighted_loc`); `risk_tier`
   reuses `/pr-risk`'s tier vocabulary; and the deterministic rules for deriving a
   descriptor from a task doc + manifest fields are written down in
   `cmd/dispatch/docs/DESIGN.md`. Freezing the enum lets the schema validator
   reject a typo'd `task_clas` at exit 2 instead of letting it silently never match.

Layout follows the workbench charter: `cmd/dispatch/internal/*` is private to this
tool; nothing imports another tool's decision logic; the placement type does **not**
enter `contracts/` yet (deferred to the phase-4 prep integration, when a second
consumer exists).

## Acceptance

- Determinism: identical descriptor + policy → byte-identical placement; no clock,
  network, or randomness in the decide path.
- Every exit code is reachable and asserted: 0 (match), 2 (bad/empty/enum-typo
  policy), 3 (no match, values on stderr), 4 (bad descriptor), 5 (requested
  receipt append failed, nothing on stdout).
- Receipt names the matched rule and carries the policy sha256; placement stdout
  carries its own `schema_version`.
- **Receipt-presence assertion**: after N successful `decide` invocations with
  `--receipts`, the receipts file has exactly N lines (the phase-2 gate runs on
  this data — an incomplete receipts file is a silent gate corruption).
- `validate` flags an empty `rules: []`, a missing catch-all (warning), and an
  unknown `task_class`.

## Test plan

Table-driven, beside each package:
- `policy`: load/validate matrix (valid, malformed JSON, empty rules, unknown
  task_class, hash-stability across an edit).
- `placement`/`match`: first-match ordering, catch-all, no-match → exit 3 with
  the right unmatched values, `schema_version` present.
- `receipt`: append success (line count == invocations), forced append failure →
  exit 5 + empty stdout, ordering (receipt written before stdout).
- `main`: end-to-end exit-code assertions per verb; a negative-control descriptor
  the shipped example policy must exit-3 on.

## Non-goals

- `drift` verb, scorecard ingest (phase 3).
- `/work-driver-prep` integration, descriptor derivation *tooling* (phase 4 — the
  derivation *rules* are documented here, but nothing calls `decide` yet).
- Promoting `placement` into `contracts/` (phase 4 trigger).
- Concurrent evidence-grade receipt writes — declared unsupported in TDD §8;
  callers are serial (human-cadence prep, serial replay harness).
