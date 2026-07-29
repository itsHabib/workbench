**Status**: draft
**Owner**: @codex:michael
**Date**: 2026-07-29
**Related**: dossier task `codexguard-policy-engine` (`tsk_01KYP78S45H49XBVAS8WWQPB1R`)

# Deterministic Codex guard policy

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Production | `cmd/codexguard/` | ~320 | 320 |
| Tests | `cmd/codexguard/` | ~500 | 250 |
| **Total** | | | **~570** |

Band: **ideal**.

## Goal

Create one real policy owner for authority-bearing Codex command and tool-call
shapes. It emits `AutoDecisionV1`; it is not a pass-through wrapper.

## Behavior / fix

- Normalize supported Bash, PowerShell, local-function, and MCP envelopes.
- Maintain an explicit supported-envelope registry. Treat opaque
  authority-capable wrappers as park/refuse rather than guessing.
- Classify safe reads/tests, grant minting, Gate/custody mutation, force push,
  repository deletion, visibility changes, `--admin`, and PR merges.
- Permit only a fully validated Gate-shaped merge carrying a full
  `--match-head-commit` SHA.
- Emit pass, park, block, or refuse with the exact rule and remedy.
- Fail closed on malformed, ambiguous, or unknown authority-bearing inputs.
- Keep merge policy in Gate; this component enforces invocation shape only.

## Acceptance

Equivalent shell/tool representations produce the same decision. Fixtures cover
`bash|sh -c/-lc`, compound commands and opaque substitutions; PowerShell
`-Command`/`-EncodedCommand`, call operator, aliases, `Start-Process`, and
`Invoke-Expression`; `cmd /c`; and nested code-mode MCP/local calls. A bare
merge, mint, state mutation, force push, deletion, visibility change, or
`--admin` cannot bypass the deterministic floor.

## Test plan

Table/property tests, an opposite-mutation demonstration, full Workbench checks,
and hygiene.

## Non-goals

No hooks, installation, branch protection, grant minting, or model approval.
