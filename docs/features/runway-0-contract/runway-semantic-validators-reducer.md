**Status**: draft
**Owner**: @itsHabib
**Date**: 2026-07-10
**Related**: dossier task `runway-semantic-validators-reducer` (id: `tsk_01KX764HZ0N5AGKYDE4GHYX2EM`), depends on `runway-contract-schemas-types`, [execution-runtime TDD](../execution-runtime/spec.md)

# Semantic admission validators + pure history reducer tests — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `contracts/execution/validate.go` | ~200 | 200 |
| Tests + fixtures | `contracts/execution/validate_test.go`, `contracts/execution/reduce_test.go` (or a `history` test package), generated/golden cases | ~350 | 175 |
| **Total** | | | **~375** |

Band: **amazing** per repo's PR sizing convention.

## Goal

Enforce in Go the semantic laws JSON Schema cannot express, closing Gate A of the execution-runtime TDD (§11). Two deliverables: (1) admission validation functions over the decoded contract types, (2) a pure history reducer test package proving the event/terminal laws without any executable-controller code.

## Behavior

### Admission validators (`contracts/execution/validate.go`)

Pure functions over the types from `runway-contract-schemas-types` — no I/O, no decision logic beyond the contract laws themselves (this is contract-law validation, permitted in the leaf package; routing/lifecycle policy stays in the future `cmd/runway`):

- **Path laws (FR3, Gate A):** reject absolute paths and `..` traversal in bundle `source`s, input `target`s, output `path`s, the top-level `cwd` `{root, value}` reference, and every `{path: {root, value}}` variant inside `command.args`/`command.executable`; roots restricted to `workspace | inputs | out`.
- **Secret ref grammar (D8):** refs must match `^env:[A-Za-z_][A-Za-z0-9_]*$`; anything else — inline values, malformed refs, empty names — rejects. Secret *values* must be unrepresentable by construction.
- **Profile hygiene (FR15):** `placement.backend` and `placement.profile` are logical names — reject anything containing path separators, `..`, or host-path shapes. `backend` remains open vocabulary (no allowlist here; adapter resolution is the controller's job).
- **Workspace immutability:** `git` workspaces require a full 40-hex revision; symbolic refs reject.
- **Terminal combination laws (§5 Result):** valid `(status, terminal_phase, reason_code)` combinations only — e.g. `succeeded` requires `completed`/`terminal`; `timed_out` requires `deadline_exceeded`; `placement_unavailable` pairs with phase `startup` (per Flow B); `causes[]` entries must themselves be valid `(phase, reason_code)` pairs.
- **Digest shape:** `sha256` fields are 64-hex; request digest laws (digest changes when any submitted byte changes) covered by a byte-mutation test.

### Pure history reducer/model tests

A reducer `Reduce([]RunEvent) (HistoryState, error)` living in `contracts/execution` itself (`reduce.go` + `reduce_test.go`, same package — no test-only sub-package), where `HistoryState` is a small struct holding the reduced view: current phase, last seq, whether terminal was reached, and the terminal event if any. Property-style tests prove, with zero controller code:

- `seq` contiguous from 1; duplicates and gaps reject.
- Phase order monotone over the canonical phase enum; regression rejects.
- At most one `run_terminal`, and it is final; events after terminal reject.
- Valid histories for each flow shape in the TDD (success, placement backpressure, timeout, cancel, controller loss) reduce cleanly; generated invalid mutations reject.

Per the TDD Phase 0 gate: these tests prove transition/result combinations **without claiming JSON Schema enforces history**.

### Gate A checklist items closed here

- Go admission validation rejects absolute/traversing bundle sources, cwd/path arguments, input targets, and outputs.
- Secret references match the `env:` grammar in both schema (prior task) and Go validation; inline/malformed reject.
- Profile names remain logical, no host paths.
- Request digest changes when any exact submitted byte changes; work digest identical across local and rooms placed requests (fixture pair differing only in `placement`).
- Reducer tests enforce contiguous, phase-monotone histories and at most one terminal event/result.
- Terminal status/reason/phase/cause combinations validated.

## Acceptance

- `go test ./contracts/execution/` green (everything lives in the one package — no sub-packages); every Gate A bullet above has at least one failing-case test.
- Leaf-package law holds (imports at most stdlib; CI `hygiene` green).
- A fixture pair (identical `work.json`, different `placement`) proves `work_sha256` equality across placements.

## Test plan

Table-driven `TestValidateWorkSpec_*` / `TestValidateRequest_*` / `TestValidateResult_*` rejection suites; `TestReduce_*` history suites including generated mutations (shuffle seq, regress phase, double-terminal). No `ValidateEvent` is expected: discrete event-field validation is JSON Schema's job (prior task); ordering laws are the reducer's. The work-digest-equality-across-placements law is owned by a `TestWorkDigestPlacementInvariant` fixture-pair test here.

## Non-goals

- Native path *expansion* and `RUNWAY_*` env parity — that is backend behavior, Phase 1 (`runway-local-rundir-journal-backend`); Gate A only pins the structured-reference laws the expansion consumes.
- Any journal/file I/O — the reducer is pure.
- Windows/Linux expansion fixtures beyond what validation needs — full expansion fixtures land with Phase 1.
