**Status:** draft
**Owner:** @itsHabib
**Date:** 2026-07-28
**Related:** Dossier task `fresh-codex-exact-head-closure-dogfood` (`tsk_01KYMQJCD7PMB767VE7Y78MD4D`)

# Fresh Codex exact-head closure dogfood

## Goal

Prove the completed Codex path on one real PR created by a Ship **cloud**
stream, with an actionable sourced finding and no manual PR-branch checkout or
push. The closure receipt must record and verify the cloud runtime.

## Procedure

1. Start a fresh Codex task and invoke the catalog-installed native producer.
2. Verify from Ship's durable run/stream state that the selected PR originated
   from a cloud stream; pin the live reviewed head and emit
   `ReviewFindingsV1`.
3. Submit it once to `ship driver address`.
4. Probe stale-head, malformed, replayed, cycle-exhausted, empty/unsourced, and
   source/panel-inconsistent artifacts; Ship itself must refuse every probe
   before dispatch.
5. Run `ship driver run <driver-run-id>` until the addressed stream reaches
   terminal success. Only then read and record the new PR head; do not manually
   checkout or push its branch.
6. Run fresh exact-head review; demonstrate that an incomplete configured panel
   parks.
7. Resolve the recorded park through the provider-neutral judgment seam using a
   judgment bound to the parked run, grant, repository, PR, and head. Prefer the
   configured Codex auto-judge; stop for operator judgment if the provider
   cannot decide. Branch on the judgment command's exit code and reduced result;
   do not create a new Gate run for the same incomplete panel.
8. Only after the judged run passes, execute the judgment result's exact emitted
   `gh pr merge ... --match-head-commit ...` command.
9. Run `ship driver land <driver-run-id> --pr <n>` immediately after the
   commit-pinned merge. Its already-merged readback path must record the merge
   SHA/time, finalize the authoritative closure receipt, and reach terminal
   closure; it must not issue a second merge.
10. Link the receipt, run, stream, PR, Gate, judgment, merge, producer, catalog revision,
   model, and interventions in Dossier.

If no implementation PR receives an actionable finding, use a canary PR with a
genuine reversible defect. The defective head must never merge.

## Acceptance

- The receipt proves the PR originated from a Ship cloud stream.
- Exact-head artifact is accepted once; every refusal probe in step 4 is
  rejected before dispatch.
- The addressed stream reaches terminal success and changes the same PR's head
  without operator checkout/push.
- The new-head panel park is resolved by a bound provider-neutral judgment that
  authorizes that exact unchanged head.
- Ship's land path reads back the Gate-pinned merge and finalizes the receipt.
- Receipt is reconstructable and contains zero mechanism-repair interventions.
- Operator action occurs only for a Gate-requested grant or genuine judgment.

## Retention checkpoint

Record whether `cmd/reviewfindings` earned its binary surface through live
pagination, exact-head filtering, and contract validation. If not, open a
focused deletion follow-up while preserving the shared contract.

## Non-goals

Invoking Claude or claiming the independent Claude-seat Gate B leg.
