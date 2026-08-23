---
driver_version: 1
generated_at: 2026-08-13T07:35:00Z
generated_by: work-driver-prep
source:
  project: cross-repo (gate ×3, ship, dossier, workbench, roxiq)
  phase: sweep-followups-2026-08-13
repo: workbench
repo_url: https://github.com/itsHabib/workbench
branch_prefix: sweep-followups-
default_runtime: local

# CROSS-REPO EXTENSION: streams carry their own `repo:` + `repo_path:`; a
# stream's worktree/branch/PR lives in THAT repo. The top-level repo fields
# only locate this manifest. Spec paths are relative to the stream's repo.

batches:
  - id: 1
    label: ready now — four repos, zero overlap
    depends_on: []
    status: pending
    streams:
      - task_id: tsk_01KZXTZ483R6B7FGSABT8B1QYY
        task_slug: sweep-windows-lifecycle-p2s
        repo: ship
        repo_path: ~/dev/ship
        spec_path: docs/features/sweep-followups-2026-08-13/sweep-windows-lifecycle-p2s.md
        branch_name: sweep-followups-windows-lifecycle
        runtime: local
        model: opus
        effort: extra
        touches: [packages/mcp-server/src/client-liveness.ts, packages/mcp-server/src/bin.ts, packages/mcp-server/src/single-instance.ts]
        status: pending
      - task_id: tsk_01KZXTZGMVTCJYD852JT67W56C
        task_slug: sweep-ci-test-aggregator
        repo: dossier
        repo_path: ~/dev/dossier
        spec_path: docs/features/sweep-followups-2026-08-13/sweep-ci-test-aggregator.md
        branch_name: sweep-followups-ci-test-aggregator
        runtime: local
        model: sonnet
        effort: extra
        touches: [.github/workflows]
        status: pending
        operator_step: "first merge cannot satisfy current protection (bare `test` context missing until landed) — operator temporarily edits required contexts or admin-merges this ONE PR; documented in the PR body"
      - task_id: tsk_01KZXV003RKDK02WEZW031SY7Z
        task_slug: sweep-protection-notes-doc
        repo: workbench
        repo_path: ~/dev/workbench
        spec_path: docs/features/sweep-followups-2026-08-13/sweep-protection-notes-doc.md
        branch_name: sweep-followups-protection-notes
        runtime: local
        model: sonnet
        effort: extra
        touches: [docs/]
        status: pending
      - task_id: tsk_01KZXV4PRH11RXR59BTWC0X6T7
        task_slug: sweep-killgroup-residual-comment
        repo: roxiq
        repo_path: ~/dev/roxiq
        spec_path: docs/features/sweep-followups-2026-08-13/sweep-killgroup-residual-comment.md
        branch_name: sweep-followups-killgroup-comment
        runtime: local
        model: sonnet
        effort: extra
        touches: [internal/gauntlet/live_session.go]
        status: pending
  - id: 2
    label: cmd/gate pair — AFTER the in-flight sweep-friction batch merges
    depends_on: [1]
    status: pending
    streams:
      - task_id: tsk_01KZXTYKG0B7A3HSKTE4D15BZT
        task_slug: sweep-pin-diff-to-head
        repo: workbench
        repo_path: ~/dev/workbench
        spec_path: docs/features/sweep-followups-2026-08-13/sweep-pin-diff-to-head.md
        branch_name: sweep-followups-pin-diff-to-head
        runtime: local
        model: opus
        effort: extra
        touches: [cmd/gate (judge.go / evidence pkg), cmd/gate/docs/FOLLOWUPS.md]
        status: pending
      - task_id: tsk_01KZXTY6MNR48683JZV826CQY0
        task_slug: sweep-next-shows-cycles
        repo: workbench
        repo_path: ~/dev/workbench
        spec_path: docs/features/sweep-followups-2026-08-13/sweep-next-shows-cycles.md
        branch_name: sweep-followups-next-shows-cycles
        runtime: local
        model: sonnet
        effort: extra
        touches: [cmd/gate (next/render path)]
        status: pending
  - id: 3
    label: new gate verb — after batch 2 (CLI wiring overlaps everything)
    depends_on: [2]
    status: pending
    streams:
      - task_id: tsk_01KZXTXQSMRQE6NZ5F72T1R1TY
        task_slug: sweep-grant-batch-verb
        repo: workbench
        repo_path: ~/dev/workbench
        spec_path: docs/features/sweep-followups-2026-08-13/sweep-grant-batch-verb.md
        branch_name: sweep-followups-grant-batch-verb
        runtime: local
        model: opus
        effort: extra
        touches: [cmd/gate/main.go, cmd/gate (new verb + grant plumbing)]
        status: pending

conflict_notes:
  - kind: file_overlap
    file: workbench cmd/gate/*
    tasks: [sweep-pin-diff-to-head (evidence/judge area), sweep-next-shows-cycles (next/render area), sweep-grant-batch-verb (main.go wiring + grant plumbing)]
    note: "pin-diff and next-cycles are textually disjoint areas of cmd/gate and could parallel-run with low rebase risk (the batch-2 pairing); grant-batch-verb adds a verb, which touches main.go wiring that any CLI-adjacent change can collide with — serialized as batch 3"
  - kind: dep_signal
    from: "batch 2 + 3 (all cmd/gate work)"
    to: "the IN-FLIGHT gate/sweep-friction-2026-08-12 batch (7 drive-UI workers, PRs 227/228 + 5 pending)"
    reason: "sweep-friction workers are actively editing cmd/gate (evidence, dispose, threads, preflight); starting more cmd/gate streams before those merge guarantees rebase pain — batch 1 has no such exposure"
  - kind: dep_signal
    from: sweep-protection-notes-doc
    to: sweep-ci-test-aggregator
    reason: "the doc's dossier row states 'drifted until the aggregator-job fix lands' — soft ordering only; the row is written either way, so both sit in batch 1 with the note"

skipped_during_resolution: []
---

# Sweep-followups 2026-08-13 driver manifest

Generated by `/work-driver-prep <7 task ids>` on 2026-08-13.
Cross-repo batch: gate(→workbench cmd/gate) ×3, ship, dossier, workbench-docs, roxiq.

## Batches

**Batch 1 — ready now, 4 parallel streams, four different repos (zero overlap):**
- ship: `sweep-windows-lifecycle-p2s` (opus) — 4 recorded Windows deferrals from ship#235
- dossier: `sweep-ci-test-aggregator` (sonnet) — CI `test` aggregator job; **carries an operator merge step**
- workbench: `sweep-protection-notes-doc` (sonnet) — per-repo protection table in gate-flow docs
- roxiq: `sweep-killgroup-residual-comment` (sonnet) — comment-only, ~10 LOC

**Batch 2 — cmd/gate pair, after the in-flight sweep-friction batch merges:**
- `sweep-pin-diff-to-head` (opus) — head-pinned diff, closes the standing FOLLOWUPS entry
- `sweep-next-shows-cycles` (sonnet) — cycles column + will-refuse flag in `gate next`

**Batch 3 — after batch 2:**
- `sweep-grant-batch-verb` (opus) — operator sweep-mint preflight verb

## Runtime note

All local (no cloud signals). Specs are on disk in each repo, uncommitted — commit each with its impl PR, or fold into a docs PR per repo if a local `/work-driver` run needs them on origin/main first. If running through the **drive UI** instead (today's engine), the specs are read from the worker's own worktree, so no pre-commit is needed — dispatch each stream as a dossier-bound worker in its repo.
