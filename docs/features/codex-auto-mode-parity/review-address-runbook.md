# Session-native ReviewFindings address runbook

This is the Phase 1 boundary between a completed native Codex review and a
fresh isolated Codex address task. It does not call Ship, dispatch a provider,
or create the Codex task.

## Preconditions

- The run is the original implementation **child** run, not the coarse parent.
- Its stream is `pr_open` and has a panel-settled `review_cycle`.
- The artifact is ReviewFindingsV1, has sourced non-empty findings, and names
  the exact live PR head.
- `WORKBENCH_STATE_DIR` names the same absolute state root used by the
  session driver.

## Fresh-task handoff

```powershell
$env:WORKBENCH_STATE_DIR = 'C:\absolute\workbench-state'

reviewfindings address accept `
  -run dsr_IMPLEMENTATION_CHILD `
  -stream dss_IMPLEMENTATION `
  -artifact C:\absolute\review-findings.json `
  -max-cycles 3

reviewfindings address claim `
  -run dsr_IMPLEMENTATION_CHILD `
  -stream dss_IMPLEMENTATION `
  -work raw_WORK_ID
```

`accept` verifies the live head through the signed-in `gh` session and prints
the durable `AddressWorkV1`. `claim` deterministically imports the address child
and links it from the authoritative ledger. Give the fresh isolated Codex task
the emitted work file:

```text
Address the sourced findings in <state>\dsr_IMPLEMENTATION_CHILD\
review-address\raw_WORK_ID.json on the existing PR branch. Refuse if the live
PR head no longer equals source_head_sha. Push the updated branch and return
the exact new head; do not merge.
```

After task creation returns, record its identity immediately:

```powershell
reviewfindings address started `
  -run dsr_IMPLEMENTATION_CHILD -stream dss_IMPLEMENTATION `
  -work raw_WORK_ID -task CODEX_TASK_ID
```

After the child pushes, `completed` verifies and records the exact new live
head:

```powershell
reviewfindings address completed `
  -run dsr_IMPLEMENTATION_CHILD -stream dss_IMPLEMENTATION `
  -work raw_WORK_ID -head NEW_40_HEX_HEAD
```

## Recovery and refusal

```powershell
reviewfindings address resume `
  -run dsr_IMPLEMENTATION_CHILD -stream dss_IMPLEMENTATION -work raw_WORK_ID
```

- `pending` reconstructs the work safely.
- `claimed` with no task id exits 2 (parked). Reconcile the Codex task before
  doing anything; never auto-create a second child.
- `started` names the task to adopt.
- `completed` names the resulting head.

Stale heads, malformed or unsourced artifacts, source/panel mismatch, exhausted
cycles, work collisions, and repeated consumption exit 3 before dispatch.
Duplicate `accept` is not recovery: it refuses and prints the existing work
ref. `resume` is the recovery verb.

## Offline proof

No GitHub or provider traffic is required for the deterministic proof:

```powershell
go test -count=1 -run 'Test(AddressAcceptCLIConsumesOnce|PrepareReviewAddress|ReviewAddressLifecycle|MixedLedgerExcludesOldWriter|WorkbenchExecutesAddressV1Corpus)' ./cmd/reviewfindings ./driverstate
```

The cross-consumer corpus is
`contracts/reviewfindings/testdata/address-v1/manifest.json`. Ship vendors that
directory byte-for-byte and verifies the pinned case and corpus digests.
The handoff schema is
`contracts/reviewfindings/schema/review-address-work-v1.json`; driver address
events use `contracts/driverstate/schema/driver-state-v0.2.0.json`.
