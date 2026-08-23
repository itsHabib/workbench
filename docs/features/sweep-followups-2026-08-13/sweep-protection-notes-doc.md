**Status**: draft
**Owner**: @mh
**Date**: 2026-08-13
**Related**: dossier task `sweep-protection-notes-doc` (id: `tsk_01KZXV003RKDK02WEZW031SY7Z`)

# Document per-repo branch-protection shapes in the merge-gate flow docs — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Production source | workbench docs/ (gate-flow / auto-mode-defaults doc) | ~50 | 50 |
| Tests | — | 0 | 0 |
| **Total** | | | **~50** |

Band: **amazing**.

## Goal

Branch-protection behavior differs silently per repo and each sweep rediscovers it the hard way: ship requires up-to-date branches (every merge of a BEHIND PR costs refresh + CI re-run + fresh gate judgment — hit on #247, #249, #235); dossier's required contexts are drifted (bare `test` vs matrix names — being fixed by `sweep-ci-test-aggregator`); most other repos have no strict requirement. The house rule "BEHIND is not by itself proof a refresh is necessary" needs the per-repo facts next to it.

## Behavior

Add a short per-repo table to the merge-gate docs (workbench docs/ — wherever the auto-mode defaults / gate flow docs live; cross-link from the CLAUDE.md merge-gate section pattern):

repo → strict up-to-date? → required contexts → notes (e.g. ship: strict, refresh resets panel attestations; dossier: drifted until the aggregator-job fix lands).

Include the maintenance rule: **when protection changes, update the row in the same PR.**

## Acceptance

Table exists in the gate-flow doc, covers at least ship / dossier / drive / workbench / roxiq / rooms, states the maintenance rule, and is cross-linked per the CLAUDE.md merge-gate pattern. Rows must be verified against live `gh api repos/<r>/branches/<default>/protection` output at authoring time, not recalled.

## Test plan

Docs-only; verification = the live protection reads pasted/summarized in the PR body.

## Non-goals

Changing any repo's protection settings.
