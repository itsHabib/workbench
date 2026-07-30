# codexguard

Deterministic policy owner for authority-bearing Codex tool calls. It emits the
shared `contracts/automode` AutoDecisionV1 artifact and never executes the
candidate action.

## Surface

```text
codexguard decide < request.json
```

Exit codes mirror the decision: `0` pass, `1` block, `2` park, `3` refuse,
`4` operational error. A decision artifact is written on every valid request,
including parks, blocks, and refusals. Malformed request JSON exits `3` without
an artifact because no replayable input exists.

## Invariants

- Policy lives only in `internal/policy`. Lifecycle hooks and installers may
  call it; they must not copy its rules.
- The tool imports shared contracts, never another tool's decision code.
- `gate` and `gh` are fixed executable names. Callers cannot select an
  executable.
- Request JSON cannot select Gate state. The CLI accepts only its trusted
  `GATE_STATE` process configuration.
- Gate is consulted only as `gate next -state <dir> -json`; GitHub is consulted
  only as `gh pr view ... --json state,headRefOid`.
- A merge passes only when its inner argv string byte-matches the unique current
  Gate `ready_to_merge.merge_command`, its repo/PR/full SHA match that row, and
  GitHub independently reports the PR OPEN at that exact head.
- Unknown or dynamically constructed calls never pass. Every non-pass decision
  carries an exact remedy.
- Raw command text and the Gate state path are not persisted in AutoDecisionV1.
  Exact command digests preserve replay identity without retaining those bytes.

## Checks

```text
gofmt -l .
go vet ./cmd/codexguard/...
golangci-lint run ./cmd/codexguard/...
go test ./cmd/codexguard/...
```
