**Status**: implemented
**Owner**: @codex:michael
**Date**: 2026-07-29
**Related**: dossier task `codexguard-policy-projection` (`tsk_01KYP78SKH7AF7EE2J2Y9NTZA6`)

# Curated Codex policy projection

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Production/assets | `cmd/codexguard/internal/projection/`, `cmd/codexguard/assets/` | ~260 | 260 |
| Tests/docs | projection tests and install guide | ~360 | 180 |
| **Total** | | | **~440** |

Band: **amazing**.

## Goal

Make repository state the canonical source for curated Codex rules and hooks,
with a collision-safe projection into `~/.codex`.

## Behavior / fix

- Ship curated `.rules` and hook assets that call one policy owner.
- Consume the merged native hook adapter; projection owns installation
  mechanism, never a second lifecycle or decision implementation.
- Add non-mutating status/dry-run plus hash-bound staged application.
- Refuse divergent unmanaged targets and target changes between check/apply.
- Preserve user-owned entries; do not silently rewrite the existing rule
  history.
- Document Codex hook trust and fresh-task activation.
- Run real `codex execpolicy check` fixtures for Bash and PowerShell.

## Acceptance

Dry-run is non-mutating, sync is idempotent, divergent targets refuse, path
confinement holds, a temporary Codex home discovers the projection, and the
projected hook bytes equal the reviewed adapter assets.

## Test plan

Temporary-home properties, collision/race fixtures, real execpolicy checks, and
full Workbench checks.

## Non-goals

No deletion of current personal rules, enterprise policy, plugin packaging, or
generic compatibility layer.
