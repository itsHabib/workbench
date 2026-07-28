**Status:** draft
**Owner:** @itsHabib
**Date:** 2026-07-28
**Related:** Dossier task `fresh-codex-exact-head-closure-dogfood` (`tsk_01KYMQJCD7PMB767VE7Y78MD4D`)

# Fresh Codex exact-head closure dogfood

## Goal

Prove the completed Codex path on one real Ship-managed PR with an actionable,
sourced finding and no manual PR-branch checkout or push.

## Procedure

1. Start a fresh Codex task and invoke the catalog-installed native producer.
2. Pin the live reviewed head and emit `ReviewFindingsV1`.
3. Submit it once to `ship driver address`.
4. Probe stale-head, malformed, and duplicate delivery; all must refuse before
   dispatch.
5. Let Ship update the existing PR branch and record the new head.
6. Run fresh exact-head review; demonstrate that an incomplete configured panel
   parks.
7. Run Gate with an operator-minted grant and execute only its emitted
   `--match-head-commit` merge command.
8. Link the receipt, run, stream, PR, Gate, merge, producer, catalog revision,
   model, and interventions in Dossier.

If no implementation PR receives an actionable finding, use a canary PR with a
genuine reversible defect. The defective head must never merge.

## Acceptance

- Exact-head artifact accepted once and rejected on replay.
- Address changes the same PR's head without operator checkout/push.
- The new-head panel settles and Gate authorizes that exact head.
- Receipt is reconstructable and contains zero mechanism-repair interventions.
- Operator action occurs only for a Gate-requested grant or genuine judgment.

## Retention checkpoint

Record whether `cmd/reviewfindings` earned its binary surface through live
pagination, exact-head filtering, and contract validation. If not, open a
focused deletion follow-up while preserving the shared contract.

## Non-goals

Invoking Claude or claiming the independent Claude-seat Gate B leg.
