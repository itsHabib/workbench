**Status**: draft
**Owner**: @codex:michael
**Date**: 2026-07-29
**Related**: dossier task `codexguard-hook-adapter` (`tsk_01KYP78SCVDK6VTNS5QXQFZBVM`)

# Native Codex hook adapters

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Production | `cmd/codexguard/internal/hook/` | ~220 | 220 |
| Tests/fixtures | `cmd/codexguard/testdata/hooks/` | ~360 | 180 |
| **Total** | | | **~400** |

Band: **amazing**.

## Goal

Bind the deterministic policy engine to Codex's real `PreToolUse`,
`PermissionRequest`, and `PostToolUse` envelopes without copying policy into
the lifecycle adapter.

## Behavior / fix

- `PreToolUse` blocks/refuses before execution.
- `PermissionRequest` answers only when policy is already deterministic.
- `PostToolUse` emits best-effort, digest-only audit evidence and never grants
  or retroactively denies authority.
- Validate actual stdin/stdout envelopes, Windows command overrides, malformed
  input, timeouts, and adapter failures.

## Acceptance

Real hook fixtures preserve the direct policy decision projection. Authority
inputs fail closed; audit failure cannot change authorization.

## Test plan

Golden hook fixtures for all three events, Bash/PowerShell cases, timeout/error
tests, and full Workbench checks.

## Non-goals

No installation, rules file, Gate policy change, or cross-harness hook adapter.
