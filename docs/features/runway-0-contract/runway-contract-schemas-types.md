**Status**: draft
**Owner**: @itsHabib
**Date**: 2026-07-10
**Related**: dossier task `runway-contract-schemas-types` (id: `tsk_01KX7644ERXRMVTZ2ZS41Y10K1`), [execution-runtime TDD](../execution-runtime/spec.md)

# Execution contract: four JSON Schemas + partitioned Go types — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `contracts/execution/execution.go`, `contracts/execution/schema.go` | ~180 | 180 |
| Schemas (config-tier) | `contracts/execution/schema/*.json` (4 files) | ~400 | 0 |
| Tests + fixtures | `contracts/execution/execution_conformance_test.go`, golden fixtures | ~350 | 175 |
| **Total** | | | **~355** |

Band: **amazing** per repo's PR sizing convention.

## Goal

Freeze the wire shape of the Runway execution vocabulary as a new partitioned contracts domain, `contracts/execution/`, mirroring the existing verdict contracts pattern: embedded JSON Schemas as the shape document, ergonomic Go types as the in-module view, and conformance tests binding the two. Wire shape only — semantic admission laws land in the sibling task `runway-semantic-validators-reducer`.

## Behavior

Add `contracts/execution/` containing:

1. **Four JSON Schemas** (draft 2020-12) under `contracts/execution/schema/`, exactly matching §5 of the execution-runtime TDD:
   - `work-spec-v0.1.0.json` — portable work spec: structured `command` (`executable` as `{name}` XOR `{path: {root, value}}`; `args` as ordered union of `{literal}` / `{path: {root, value}}`), `cwd` as structured root reference, `workspace` (`kind: git`, url, immutable revision), `inputs[]` (`{name, source, target, sha256}`), `secrets[]` (`{name, ref}` with ref pattern `^env:[A-Za-z_][A-Za-z0-9_]*$`), `outputs[]` (`{name, path, required}`).
   - `execution-request-v0.1.0.json` — placed run request: `schema_version`, `request_id`, `work` (`{manifest, sha256}`), `placement` (`{backend, profile}` — backend is an **open string**, not an enum), `policy` (`{deadline_ms, cancel_grace_ms}`).
   - `execution-event-v0.1.0.json` — run event: `run_id`, `seq`, `time`, `phase` (enum: `admission | preparation | startup | workload | collection | cleanup | terminal`), `kind`, `message`, `details`.
   - `execution-result-v0.1.0.json` — terminal receipt: statuses `succeeded | failed | timed_out | cancelled`; reason codes per TDD §5 (including `placement_unavailable`, `controller_lost`); `causes[]` (`{phase, reason_code, message?}`), `diagnostics[]` (`{code, message, details?}`), `placement` receipt (`backend, profile, allocation_id, image_sha256?, stream_delivery, enforced, details`), `artifacts[]` (`{name, path, sha256, size}`). `stream_delivery` allows `terminal_replay | none`; `live` is reserved and NOT in the v0 enum (TDD D11).

2. **Go types** in `contracts/execution/execution.go` — the ergonomic view of the four schemas, with tolerant decoders (unknown additive fields decode; unknown major schema versions reject loudly, matching FR14). Embed the schemas via `//go:embed` in `contracts/execution/schema.go` following the existing `contracts/schema.go` pattern.

3. **Shape conformance tests** in `contracts/execution/execution_conformance_test.go` — the existing recursive `objSchema` walk pattern from `contracts/conformance_test.go`: property names, required fields, nested shapes, and enums structurally identical between schema and Go types, for all four documents.

4. **Golden fixtures** — one valid instance per schema (use the TDD §5 examples verbatim as the seed) plus representative invalid instances (wrong major version, missing required field, enum violation). Round-trip the valid fixtures through the Go types. Work-spec fixtures must cover **both** `executable` variants — `{name}` and `{path: {root, value}}` — so the XOR branch has schema and round-trip coverage. Note the shape precisely: `cwd` is a single top-level `{root, value}` reference; path-typed values inside `command.args` are discriminated-union variants — the two are distinct positions, not one concept.

Boundary law applies: `contracts/execution` is a leaf package — it imports nothing else in the module (CI `hygiene` enforces). No decision logic; types, schemas, and tolerant decoders only.

## Acceptance

- `go test ./contracts/execution/` green; conformance tests fail if any of the four schemas and its Go type drift structurally.
- Golden valid fixtures decode and re-encode losslessly; invalid fixtures reject with the expected error class.
- CI `hygiene` job still passes (leaf-package law holds).
- Schemas contain no `local`/`rooms` backend enum, no host paths, no provider (Cursor/Claude/Codex/model/prompt) fields — FR2.

## Test plan

Mirror the naming of `contracts/conformance_test.go`: per-document conformance tests (`TestWorkSpecConformance`, `TestRequestConformance`, `TestEventConformance`, `TestResultConformance`) plus golden round-trip tests and version-rejection tests.

## Non-goals

- Semantic admission validation (path traversal, secret ref grammar enforcement in Go, status/reason/phase/cause combination laws) — sibling task `runway-semantic-validators-reducer`.
- The pure history reducer/model test package — same sibling task.
- Any `cmd/runway` executable code — Phase 1.
