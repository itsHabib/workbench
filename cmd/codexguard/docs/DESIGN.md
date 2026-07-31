# codexguard design

## Job

codexguard answers one question: does this observable Codex tool-call shape
pass the deterministic auto-mode rulebook? Its output is AutoDecisionV1. It
does not execute calls, install hooks, mint authority, or decide merge policy.

## Layers

```text
request envelope
      |
      v
normalizer -------- explicit supported-envelope/tool registries
      |
      v
classifier -------- read/test, known authority mutation, merge, unknown
      |
      +-- non-merge ----------> AutoDecisionV1
      |
      +-- merge
            |
            +-- gate next -json (fixed binary, read only)
            +-- gh pr view     (fixed binary, independent live read)
            |
            v
        AutoDecisionV1
```

`normalize.go` is mechanism: it converts equivalent static representations to
one semantic operation. `command.go` and `policy.go` own policy. `exec.go` is
dumb process plumbing behind two small interfaces, so tests supply exact
fixtures without touching real Gate state or GitHub.

## Boundary law

The package imports `contracts/automode`, which is vocabulary. It does not
import `cmd/gate`: Gate remains the merge-policy owner. The join is the
artifact/CLI seam `gate next -json`. This intentional parsing copy is the same
kind of cross-tool artifact consumer used elsewhere in Workbench.

## Why byte equality

Parsing a command can show that it looks commit-pinned; it cannot prove Gate
authorized it. Therefore parsing is only the first filter. The pass condition
compares the exact inner command bytes with Gate's current recorded command.
Whitespace, merge strategy, repo, PR, head, and every option stay bound to the
reviewed artifact. GitHub then supplies an independent live-state check so an
old artifact cannot authorize a moved head. Candidate and recorded-command
digests preserve the comparison in AutoDecisionV1 without persisting raw shell
text.

## Failure semantics

- `block`: a recognized action is deterministically forbidden in a governed
  task (mint, force-push, deletion, visibility/admin change, direct authority
  state mutation).
- `refuse`: a merge request is malformed or lacks exact current evidence.
- `park`: the envelope or action is unsupported/opaque and needs a supported
  representation or operator review.
- `error`: codexguard itself could not construct or emit a valid artifact.

Gate/GitHub read failures are refusals, not errors, because authority is absent
when the necessary evidence cannot be established. Every non-pass artifact
names the fired rule and a remedy.

## Process trust boundary

The read/test allowlist rejects shell control syntax and known caller-selected
executable/output flags. It is not an OS sandbox: `go test`, `go vet`,
`golangci-lint`, Git attributes/config, and programs resolved from `PATH` still
execute repository- or machine-configured code. Credential custody and external
Gate enforcement remain the authority boundary behind these useful development
commands. A shell read pass is replayable through its command/workdir digests,
but it is not sufficient authority for an enforcing adapter to permit an
out-of-workspace or secret-path read; workspace/OS confinement remains
mandatory. Generic local `read_file`/`view_image` calls therefore park in this
slice instead of receiving a blanket pass.

Read-only MCP calls bind a digest of their normalized JSON object arguments
into the action identity. Malformed or non-object arguments park; raw argument
bytes are not persisted.

## Honest enforcement boundary

This slice is a policy engine. Codex lifecycle hooks and restrictive rule
projection are separate consumers. Until they are installed and trusted,
codexguard's classifications are callable and replayable but do not themselves
prevent a harness from executing a command.
