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
old artifact cannot authorize a moved head.

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

## Honest enforcement boundary

This slice is a policy engine. Codex lifecycle hooks and restrictive rule
projection are separate consumers. Until they are installed and trusted,
codexguard's classifications are callable and replayable but do not themselves
prevent a harness from executing a command.
