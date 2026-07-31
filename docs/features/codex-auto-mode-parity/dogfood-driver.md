---
driver_version: 1
generated_at: 2026-07-29T13:10:00Z
generated_by: work-driver-prep
source:
  project: workbench
  phase: codex-auto-mode-parity
repo: workbench
repo_url: https://github.com/itsHabib/workbench
branch_prefix: codex/auto-mode-
default_runtime: local
default_provider: codex
default_fallback: []
done_boundary: merged
ping_gates:
  - reviewer: claude
    action: omit
    authority: operator
    reason: no Claude invocation
  - reviewer: cursor
    action: omit
    authority: operator
    reason: no model-provider run or API key
runtime_notes:
  - done_boundary is consumed by the session orchestrator and persisted in the Workbench run_imported event; Ship's provider manifest parser reports this session-only field as an advisory warning
batches:
  - id: 1
    label: fresh-task live proof
    depends_on: []
    status: pending
    streams:
      - task_id: tsk_01KYP78SSC77DRNABJB8F3VAVF
        task_slug: codexguard-fresh-task-dogfood
        spec_path: docs/features/codex-auto-mode-parity/codexguard-fresh-task-dogfood.md
        branch_name: codex/codexguard-fresh-task-dogfood
        runtime: local
        model_id: gpt-5.6-sol
        effort: ultracode
        touches: [cmd/codexguard/testdata/e2e, docs/features/codex-auto-mode-parity/evidence.md, docs/features/codex-auto-mode-parity/refusal-matrix.md]
        status: pending
conflict_notes: []
---

# Codex auto-mode dogfood driver

Run this only after:

1. every stream in `driver.md` is merged; and
2. the Ship conformance stream in `ship-driver.md` is merged with the exact
   Workbench corpus digest.

```text
/work-driver docs/features/codex-auto-mode-parity/dogfood-driver.md --engine session
```

Before import, the session parent verifies both prerequisite Dossier tasks and
their merged PR heads against GitHub and records those refs in the run import
note. This explicit hand-off is the cross-repository dependency; it is not
misrepresented as an unsupported per-stream repository field.

This run inherits the operator-authorized degraded-panel policy recorded in
`driver.md`: session-native Codex review only, no Claude/Cursor trigger, with
the omission recorded for Gate. It uses no provider run or model-provider API
key.
