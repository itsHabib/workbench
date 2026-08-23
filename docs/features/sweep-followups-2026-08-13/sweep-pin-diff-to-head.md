**Status**: draft
**Owner**: @mh
**Date**: 2026-08-13
**Related**: dossier task `sweep-pin-diff-to-head` (id: `tsk_01KZXTYKG0B7A3HSKTE4D15BZT`), standing entry in cmd/gate/docs/FOLLOWUPS.md

# Pin the primary diff path to the evaluated head — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | cmd/gate evidence/diff path (judge.go / evidence pkg) | ~60 | 60 |
| Tests | race-shape tests | ~90 | 45 |
| **Total** | | | **~105** |

Band: **amazing**.

## Goal

Standing FOLLOWUPS entry ("Pin the primary diff path to the evaluated head", surfaced 2026-07-16 by the evidence-local-diff skeptic panel), independently re-found by Codex as a P2 on workbench#226 — it keeps costing review attention until closed. `gh pr diff <n>` fetches by PR number with no head pin; a force-push race records the wrong head's diff while `--match-head-commit` can still be satisfied by pushing back.

## Behavior

Implement the fix shape already written in the FOLLOWUPS entry:

- After a successful `gh pr diff`, re-read `pulls/<n>` and refuse unless `head.sha == view.headRefOid` (shrinks the race to a sub-call window).
- Optionally the airtight variant: fetch the under-cap diff SHA-pinned via the compare endpoint. The oversized-PR fallback already has this property — bring the primary path to parity with it.
- Close/update the FOLLOWUPS.md entry in the same PR.

## Acceptance

- A test that simulates a head change between diff fetch and the re-read asserts refusal (named refusal, not silent wrong-diff).
- Unchanged-head path passes as today.
- FOLLOWUPS.md no longer lists the entry as open.

## Test plan

Fake the gh seam: diff fetch returns content, pulls/<n> returns a differing head → expect refusal; same head → expect pass-through.

## Non-goals

Rewriting the oversized-PR fallback (already pinned); any change to `--match-head-commit` semantics.
