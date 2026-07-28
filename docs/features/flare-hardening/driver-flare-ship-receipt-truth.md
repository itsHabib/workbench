---
driver_version: 1
generated_at: 2026-07-28T22:20:00Z
generated_by: work-driver
source:
  project: workbench
  phase: flare-hardening
repo: workbench
repo_url: https://github.com/itsHabib/workbench
branch_prefix: flare-
default_runtime: cloud
default_provider: cursor

batches:
  - id: 1
    label: honest Ship park notifications
    depends_on: []
    status: pending
    streams:
      - task_id: tsk_01KYNBZ8HNNXBT9JRAC4GPZ81V
        task_slug: flare-ship-receipt-truth
        spec_path: docs/features/flare-hardening/flare-ship-park-receipt-truth.md
        branch_name: codex/flare-ship-receipt-truth
        runtime: cloud
        provider: cursor
        model_id: composer-2.5
        effort: extra
        touches:
          - cmd/flare/internal/source/
          - cmd/flare/internal/notify/
        status: pending

conflict_notes: []
---

# Flare Ship receipt truth driver

One cloud stream used as the Work Driver engine acceptance run. Success requires
the agent to edit the isolated Workbench worktree, run the scoped checks, and
return a terminal result with inspectable events and filesystem changes.
