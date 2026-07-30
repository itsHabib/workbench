---
driver_version: 1
generated_at: 2026-07-29T05:25:00Z
generated_by: work-driver-prep
source:
  project: workbench
  phase: flare-repo-routing
repo: workbench
repo_url: https://github.com/itsHabib/workbench
branch_prefix: codex/flare-repo-route-
default_runtime: session

batches:
  - id: 1
    label: ready now
    depends_on: []
    status: pending
    streams:
      - task_id: tsk_01KYP59QGHDCVN33DMV79CXA4E
        task_slug: flare-repo-route-filter
        spec_path: docs/features/flare-repo-route-filter/spec.md
        runtime: session
        touches:
          - cmd/flare/internal/config/config.go
          - cmd/flare/internal/config/config_test.go
          - cmd/flare/internal/route/route.go
          - cmd/flare/internal/route/route_test.go
          - cmd/flare/docs/DESIGN.md
          - cmd/flare/docs/OPERATIONS.md
        status: pending

conflict_notes: []
---

# Flare per-repository route filter driver manifest

Generated for one normal Ship-engine stream. The task is file-disjoint because
it is the only stream.
