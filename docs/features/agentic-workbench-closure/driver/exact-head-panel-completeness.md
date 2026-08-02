**Status:** shipped in PR #163 (`feat(gate): enforce exact-head review panels`)
**Owner:** @itsHabib
**Date:** 2026-07-28
**Related:** Dossier task `exact-head-panel-completeness` (`tsk_01KYMQHPRCJYZ91HYHMVMFCD2T`)

# Exact-head panel completeness

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Gate evidence/review policy | `cmd/gate/internal/evidence`, `internal/verify/reviews.go` | ~220 | 220 |
| Tests | panel-set and head-change cases | ~260 | 130 |
| Docs/contracts | scoped Gate docs; existing envelope if sufficient | ~80 | 40 |
| **Total** | | | **~390** |

Band: **ideal**.

## Goal

Make the configured reviewer panel explicit for the exact judged head so one
clean reviewer cannot launder missing or pending reviewers into a Gate pass.

## Behavior

- Resolve expected, completed, pending, and missing reviewers from a
  repository-owned declaration plus exact-head evidence.
- Use an existing versioned artifact/verdict envelope when possible.
- Do not overload `ReviewFindingsV1` to describe a clean final panel and do not
  parse prose/sticky comments as authority.
- Missing, pending, or unknown panel state escalates/parks.
- Stale-head comments never satisfy the current panel.
- Preserve existing actionable-finding behavior and reducer monotonicity.
- If the existing contracts cannot honestly express clean panel state, add a
  reviewed versioned contract rather than an ad hoc shape.

## Acceptance

- Complete clean exact-head panel may pass.
- Every incomplete/unknown permutation parks.
- An old-head clean panel cannot satisfy a new head.
- Absence is never green.

## Test plan

- Table and generated-set tests over expected/completed permutations.
- Head-advance tests.
- Full Gate and Workbench checks.

## Non-goals

Triggering reviewers, waiting policy, branch protection, or suppressing findings.
