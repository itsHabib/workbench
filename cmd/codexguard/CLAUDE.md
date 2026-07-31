# codexguard

Deterministic policy owner for authority-bearing Codex tool calls. It emits the
shared `contracts/automode` AutoDecisionV1 artifact and never executes the
candidate action.

## Surface

```text
codexguard decide < request.json
codexguard hook < native-codex-hook.json
codexguard projection status -home <codex-home>
codexguard projection apply -home <codex-home> -expect <status-digest>
```

Exit codes mirror the decision: `0` pass, `1` block, `2` park, `3` refuse,
`4` operational error. A decision artifact is written on every valid request,
including parks, blocks, and refusals. Malformed request JSON exits `3` without
an artifact because no replayable input exists.

The `hook` verb always exits `0` after a successfully handled native lifecycle
event; deny/refuse is expressed in Codex's JSON response, not the process exit
code. Adapter, persistence, or input failure exits `4` with no fabricated deny.
Codex may continue after such hook failures, so `docs/hooks.md` is the
load-bearing honest failure matrix.

Projection is a separate file mechanism over the reviewed assets. Status is
non-mutating; apply is bound to the exact observed plan and refuses divergent
or racing targets. It does not install the executable, trust hooks, or activate
a profile. See `docs/install.md`.

## Invariants

- Policy lives only in `internal/policy`. Lifecycle hooks and projection may
  call it; they must not copy its rules.
- `PreToolUse` and `PermissionRequest` sync their decision events before
  returning. Permission requests never auto-allow because the native envelope
  does not identify the approval capability.
- `PostToolUse` records best-effort completion only for a session/tool-bound
  prior pass plus explicit structured status; it never creates authority.
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
