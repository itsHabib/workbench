**Status**: draft
**Owner**: @codex
**Date**: 2026-07-28
**Related**: dossier task `gate-ci-classify-eval-command` (id: `tsk_01KYN9XMPR3TPRRA3AH155AYYZ`)

# Gate ci-classify eval command — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Eval command + path mechanism | `cmd/gate/tools/ci-classify-eval/` | ~130 | 130 |
| Tests + invocation docs | focused tests and existing eval/gateway docs | ~120 | 60 |
| **Total** | | | **~190** |

Band: **amazing**.

## Goal

Move Gate's runnable cloud-evaluation program out of the documentation tree while
keeping the frozen prompts, fixtures, evidence, and results where they belong.

## Behavior / fix

- Move `cmd/gate/docs/features/ci-classify/eval/run-cloud/main.go` to
  `cmd/gate/tools/ci-classify-eval/`.
- Keep the runner under `cmd/gate/` so it may reuse Gate's private model-backend
  mechanism without exporting decision logic or creating a cross-tool import.
- Default the eval bundle to
  `cmd/gate/docs/features/ci-classify/eval`. Resolve the module root from the
  command source location, verify it with the repository `go.mod`, and expose an
  explicit `-repo-root` override. Do not silently depend on the process working
  directory.
- Preserve `-eval-dir` and `-out` overrides and the emitted
  `{expected,meta,output}` JSONL format. Relative explicit paths remain relative
  to the caller; only repository-owned defaults are rooted automatically.
- Keep prompts, schema, fixtures, scoring scripts, raw output, and result notes
  under the existing documentation feature directory.
- Update all invocation references and remove the old Go package directory.

## Acceptance

- No `.go` package remains under
  `cmd/gate/docs/features/ci-classify/eval/run-cloud`.
- The documented command works from the module root and from another working
  directory without changing eval inputs or output format.
- Root discovery fails with a named, actionable error if its source-derived root
  or explicit override is not the Workbench module.
- Existing cloud backend, gateway environment variables, prompts, fixtures,
  scoring thresholds, and results are unchanged.

## Test plan

- Unit tests for module-root validation, default eval-dir resolution, explicit
  overrides, and non-root working directories.
- A fixture-only command test that reaches input loading without making a model
  call; do not spend live inference in ordinary tests.
- Run `gofmt -l .`, `go vet ./...`, `golangci-lint run ./...`, and `go test ./...`.

## Non-goals

- Changing classifier policy, prompts, thresholds, fixtures, provider semantics,
  or recorded gateway results.
- Generalizing a framework for arbitrary evals.

