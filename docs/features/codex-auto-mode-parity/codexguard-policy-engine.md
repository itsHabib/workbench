**Status**: implementation complete on `codex/codexguard-policy-engine`; merge pending
**Owner**: @codex:michael
**Date**: 2026-07-29
**Related**: dossier task `codexguard-policy-engine` (`tsk_01KYP78S45H49XBVAS8WWQPB1R`)

# Deterministic Codex guard policy

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Production | `cmd/codexguard/` | ~380 | 380 |
| Tests | `cmd/codexguard/` | ~560 | 280 |
| **Total** | | | **~660** |

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
- Treat shape as necessary but never sufficient for a merge. Resolve Gate
  provenance only through Gate's read-only CLI/artifact seam: the candidate
  argv must byte-match the `merge_command` on the current
  `ready_to_merge` row from `gate next -json`, including repo, PR, and full
  `--match-head-commit` SHA.
- Independently fetch the live PR and require OPEN plus the same exact head.
  Missing state configuration, no unique ready row, a newer terminal Gate
  outcome, GitHub ambiguity, stale head, or any argv difference refuses with
  the exact re-run-Gate remedy.
- Emit pass, park, block, or refuse with the exact rule and remedy.
- Fail closed on malformed, ambiguous, or unknown authority-bearing inputs.
- Keep merge policy in Gate: `codexguard` imports no Gate decision code and
  never reconstructs a merge command; it verifies the proposed invocation
  against Gate's persisted newest terminal action.

## Acceptance

Equivalent shell/tool representations produce the same decision. Fixtures cover
`bash|sh -c/-lc`, compound commands and opaque substitutions; PowerShell
`-Command`/`-EncodedCommand`, call operator, aliases, `Start-Process`, and
`Invoke-Expression`; `cmd /c`; and nested code-mode MCP/local calls. A bare
merge, mint, state mutation, force push, deletion, visibility change, or
`--admin` cannot bypass the deterministic floor. A forged full-SHA command, an
older superseded `would_merge`, a moved head, and an ambiguous GitHub read all
refuse; only the exact current Gate-recorded command for the exact live head
passes.

## Test plan

Table/property tests, an opposite-mutation demonstration, full Workbench checks,
and hygiene.

## Non-goals

No hooks, installation, branch protection, grant minting, Gate policy
duplication, or model approval.

## Implemented surface

`cmd/codexguard` owns the deterministic rulebook and emits
`contracts/automode.Decision`. Its fakeable read-only seams invoke only the
fixed `gate next -json` and `gh pr view` commands. The test suite covers the
envelope matrix, opposite mutations of an authorized merge, stale/ambiguous
evidence, and the rule that no raw command or state path enters the artifact.

This status describes repository code only. Hook binding and policy projection
remain separate dependent slices, so no installed-enforcement claim is made.
