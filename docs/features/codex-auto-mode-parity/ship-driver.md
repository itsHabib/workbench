---
driver_version: 1
generated_at: 2026-07-29T13:10:00Z
generated_by: work-driver-prep
source:
  project: ship
  phase: codex-auto-mode-parity
repo: ship
repo_url: https://github.com/itsHabib/ship
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
    label: review-findings cross-consumer conformance
    depends_on: []
    status: pending
    streams:
      - task_id: tsk_01KYPYX4EQTVC8S3Z1HAQXSZXK
        task_slug: reviewfindings-shared-conformance-corpus
        spec_path: docs/features/codex-auto-mode-parity/ship-reviewfindings-conformance.md
        branch_name: codex/reviewfindings-conformance
        runtime: local
        model_id: gpt-5.6-sol
        effort: max
        touches: [packages/driver/testdata/reviewfindings-address-v1, packages/driver/src/review-findings.test.ts, packages/driver/src/engine.test.ts, packages/store/src/review-artifacts.test.ts]
        status: pending
conflict_notes: []
---

# Ship ReviewFindings conformance driver

Run this only after the Workbench
`session-reviewfindings-address-boundary` stream from `driver.md` is merged and
its canonical corpus digest is readable on `origin/main`:

```text
/work-driver docs/features/codex-auto-mode-parity/ship-driver.md --engine session
```

The manifest is stored in Workbench so the session parent must pass the
absolute Workbench spec path to the isolated Ship child. The parent run and
worktree target are Ship, matching the canonical one-repository driver schema.

This run inherits the operator-authorized degraded-panel policy recorded in
`driver.md`: session-native Codex review only, no Claude/Cursor trigger, with
the omission recorded for Gate. It uses no provider run or model-provider API
key.
