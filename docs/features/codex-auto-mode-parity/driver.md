---
driver_version: 1
generated_at: 2026-07-29T06:00:13Z
generated_by: work-driver-prep
source:
  project: workbench
  phase: codex-auto-mode-parity
repo: workbench
repo_url: https://github.com/itsHabib/workbench
branch_prefix: codex/auto-mode-
default_runtime: local
done_boundary: merged
batches:
  - id: 1
    label: independent shared foundations
    depends_on: []
    status: pending
    streams:
      - task_id: tsk_01KYP78RWR1HJ0P8KWEZ0QJMA2
        task_slug: auto-decision-v1-contract
        spec_path: docs/features/codex-auto-mode-parity/auto-decision-v1-contract.md
        branch_name: codex/auto-decision-v1-contract
        runtime: local
        model: gpt-5.6-sol
        effort: high
        touches: [contracts/automode]
        status: pending
      - task_id: tsk_01KYP81VP40DX668F3G6JCCKGR
        task_slug: session-reviewfindings-address-boundary
        spec_path: docs/features/codex-auto-mode-parity/session-reviewfindings-address-boundary.md
        branch_name: codex/session-reviewfindings-address
        runtime: local
        model: gpt-5.6-sol
        effort: max
        touches: [contracts/reviewfindings, contracts/driverstate, cmd/reviewfindings, cmd/driverstate, docs/features/session-orchestrator]
        status: pending
  - id: 2
    label: policy owner and cross-repo conformance
    depends_on: [1]
    status: pending
    streams:
      - task_id: tsk_01KYP78S45H49XBVAS8WWQPB1R
        task_slug: codexguard-policy-engine
        spec_path: docs/features/codex-auto-mode-parity/codexguard-policy-engine.md
        branch_name: codex/codexguard-policy-engine
        runtime: local
        model: gpt-5.6-sol
        effort: max
        touches: [cmd/codexguard]
        status: pending
      - task_id: tsk_01KYPYX4EQTVC8S3Z1HAQXSZXK
        task_slug: reviewfindings-shared-conformance-corpus
        repo: ship
        repo_url: https://github.com/itsHabib/ship
        spec_path: docs/features/codex-auto-mode-parity/ship-reviewfindings-conformance.md
        branch_name: codex/reviewfindings-conformance
        runtime: local
        model: gpt-5.6-sol
        effort: high
        touches: [packages/driver/testdata/reviewfindings-address-v1, packages/driver/src/review-findings.test.ts, packages/driver/src/engine.test.ts, packages/store/src/review-artifacts.test.ts]
        status: pending
  - id: 3
    label: native hook adapter
    depends_on: [2]
    status: pending
    streams:
      - task_id: tsk_01KYP78SCVDK6VTNS5QXQFZBVM
        task_slug: codexguard-hook-adapter
        spec_path: docs/features/codex-auto-mode-parity/codexguard-hook-adapter.md
        branch_name: codex/codexguard-hook-adapter
        runtime: local
        model: gpt-5.6-sol
        effort: high
        touches: [cmd/codexguard/internal/hook, cmd/codexguard/testdata/hooks, cmd/codexguard/assets/hooks.json]
        status: pending
  - id: 4
    label: collision-safe policy projection
    depends_on: [3]
    status: pending
    streams:
      - task_id: tsk_01KYP78SKH7AF7EE2J2Y9NTZA6
        task_slug: codexguard-policy-projection
        spec_path: docs/features/codex-auto-mode-parity/codexguard-policy-projection.md
        branch_name: codex/codexguard-policy-projection
        runtime: local
        model: gpt-5.6-terra
        effort: high
        touches: [cmd/codexguard/internal/projection, cmd/codexguard/assets/rules, cmd/codexguard/docs/install.md]
        status: pending
  - id: 5
    label: fresh-task live proof
    depends_on: [2, 4]
    status: pending
    streams:
      - task_id: tsk_01KYP78SSC77DRNABJB8F3VAVF
        task_slug: codexguard-fresh-task-dogfood
        spec_path: docs/features/codex-auto-mode-parity/codexguard-fresh-task-dogfood.md
        branch_name: codex/codexguard-fresh-task-dogfood
        runtime: local
        model: gpt-5.6-sol
        effort: max
        touches: [cmd/codexguard/testdata/e2e, docs/features/codex-auto-mode-parity/evidence.md, docs/features/codex-auto-mode-parity/refusal-matrix.md]
        status: pending
conflict_notes:
  - kind: dep_signal
    from: codexguard-policy-engine
    to: auto-decision-v1-contract
    reason: the policy engine emits the merged contract
  - kind: dep_signal
    from: codexguard-hook-adapter
    to: codexguard-policy-engine
    reason: the hook adapter contains no independent decision policy
  - kind: file_overlap
    file: cmd/codexguard
    tasks: [codexguard-hook-adapter, codexguard-policy-projection]
  - kind: dep_signal
    from: codexguard-policy-projection
    to: codexguard-hook-adapter
    reason: projection ships the reviewed native hook assets
  - kind: dep_signal
    from: codexguard-fresh-task-dogfood
    to: codexguard-policy-projection
    reason: a fresh task can test only installed reviewed policy
  - kind: dep_signal
    from: codexguard-fresh-task-dogfood
    to: session-reviewfindings-address-boundary
    reason: live exact-head acceptance must use the session-native boundary
  - kind: dep_signal
    from: reviewfindings-shared-conformance-corpus
    to: session-reviewfindings-address-boundary
    reason: Ship vendors and executes the canonical address-v1 corpus
  - kind: dep_signal
    from: codexguard-fresh-task-dogfood
    to: reviewfindings-shared-conformance-corpus
    reason: live proof begins only after both lifecycle consumers pass the corpus
---

# Codex auto-mode parity driver

Generated by `/work-driver-prep` on 2026-07-29. Consume with:

```text
/work-driver docs/features/codex-auto-mode-parity/driver.md --engine session
```

## Batches

1. Versioned leaf decision contract and session-native review address boundary.
2. Deterministic policy-owning `codexguard` binary and paired Ship conformance.
3. Native Codex lifecycle adapters.
4. Collision-safe `.rules` and hook projection.
5. Fresh-task refusal, Gate-shaped merge, and live session review-artifact dogfood.

This seven-stream cross-repository manifest is local/session-only. It must not
be imported into Ship's cloud driver. No stream may use a model-provider API key
or invoke Claude.
