**Status**: draft
**Owner**: @michael
**Date**: 2026-07-13
**Related**: dossier task `explain-json-projection` (id: `tsk_01KXDYFHTRQ58HDTXPH0ETCAQV`, workbench project, talk-readiness phase)

# explain -json: shared read-only projection of the decision chain — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | `internal/observe/observe.go`, `cmd/gate/main.go` | ~160 | 160 |
| Tests | `internal/observe/observe_test.go`, `cmd/gate/main_test.go` | ~180 | 90 |
| **Total** | | | **~250** |

Band: **amazing** per repo's PR sizing convention.

## Goal

`gate explain` renders text straight to a writer: `observe.Explain(w, st, run)` walks `st.Run(run)` and `fmt.Fprintf`s per artifact kind. There is no machine-readable form, so any UI or visualization that wants the evidence→verdict→judgment→action chain would have to reparse `state.Artifact` bodies itself — reintroducing the parser-per-consumer problem the workbench `contracts` package exists to kill. Refactor `internal/observe` into projection + rendering, and expose the projection as JSON via a new `-json` flag on `explain`.

## Behavior / fix

- A structured projection type (e.g. `Run{Run string; Artifacts []Node}`, where `Node` carries id/kind/time/parents plus the kind-specific fields `describe*` currently extracts: evidence summary; verdict source/producer/decision/tier/confidence/why/findings; flat grant/escalation/action bodies). Built purely from state artifacts — same storeless rule the package doc states.
- `Explain` (text) renders FROM the projection; output stays byte-identical to today, except flat grant/escalation/action lines, which previously rendered in nondeterministic map-iteration order and now follow the artifact's own JSON key order (deterministic, golden-pinnable).
- A JSON path that marshals the projection, stable field names.
- `cmd/gate/main.go`: add `-json` to the `explain` flagset (`cmdExplain`), default off.

## Acceptance

- `gate explain -run X` output is byte-identical pre/post for single-key flat bodies and all non-flat kinds; multi-key flat bodies render in artifact JSON key order (the pre-change order was nondeterministic, so byte-identity was never well-defined there). Pinned by a golden test over a fixture store.
- `gate explain -run X -json` emits one JSON document; each node carries id, kind, time, parents, and its kind-specific fields; unparseable bodies are represented explicitly (not dropped).
- No consumer-facing parsing of `state.Artifact.Body` outside `internal/observe`.

## Test plan

`go test ./internal/observe/... ./cmd/gate/...` — golden text-parity test + a JSON round-trip test over a fixture run containing all six artifact kinds (evidence, verdict, judgment, grant, escalation, action). `go vet ./...`, `golangci-lint run ./...`.

## Non-goals

Any HTML/visual rendering (sibling task `decision-trace-view`); changes to `audit`; new artifact kinds; control-room integration.
