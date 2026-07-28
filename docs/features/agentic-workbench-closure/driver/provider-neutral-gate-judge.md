**Status:** draft
**Owner:** @itsHabib
**Date:** 2026-07-28
**Related:** Dossier task `provider-neutral-gate-judge` (`tsk_01KYMQGTQ9J7SJ2S64YWMATPSA`)

# Provider-neutral Gate judgment seam

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Gate mechanism and CLI | `cmd/gate/internal/verify/judge.go`, `cmd/gate/main.go` | ~220 | 220 |
| Tests | scoped Gate tests and properties | ~260 | 130 |
| Docs | Gate README/design | ~60 | 30 |
| **Total** | | | **~380** |

Band: **ideal**.

## Goal

Remove Gate's implicit dependency on the Claude CLI while preserving its
judgment/reducer laws and giving a Codex seat an exact-run, exact-head artifact
submission path.

## Behavior

- Replace the hard-coded `claude -p` call with a versioned provider-neutral
  judgment input/output contract.
- Bind submitted judgment to the parked run, recorded subject/head,
  escalation/question, and the presented grant ceiling.
- Validate completely before appending to Gate's hash-chained state.
- Keep operator manual judgment available.
- `judge -auto` may run only an explicitly configured provider command that
  implements the same contract; without one it refuses with a typed actionable
  error. There is no implicit Claude fallback.
- Preserve exit codes, reducer policy, grant authority, and exact-head merge
  command behavior.

## Acceptance

- Production Gate code neither names nor shells Claude.
- A valid Codex-produced exact-run/head artifact is accepted.
- Malformed, wrong-run, stale-head, tier-exceeding, and duplicate inputs refuse
  without state mutation.
- Documentation gives the Codex-only invocation and explicit configured-command
  invocation.

## Test plan

- Table tests for every refusal.
- Generated/property coverage for validate-before-append and at-most-one
  judgment.
- `go test ./cmd/gate/...`, `go vet ./...`, lint, and full repository tests.

## Non-goals

Grant minting changes, branch protection, a model SDK, or provider policy.
