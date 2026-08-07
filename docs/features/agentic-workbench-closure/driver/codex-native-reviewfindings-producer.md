**Status:** shipped (Workbench producer slice) in PR #156 (`feat(reviewfindings): add Codex exact-head producer`) — the cc-skills catalog projection (install/call from the canonical Codex review skill catalog, [`driver.md`](../driver.md)) and its acceptance checks ([`codex-review-findings.md`](../codex-review-findings.md)) remain open.
**Owner:** @itsHabib
**Date:** 2026-07-28
**Related:** Dossier task `codex-native-reviewfindings-producer` (`tsk_01KYMQGTM65YSFX1QF3J0RZ5Y0`), Workbench PR #156, Ship PRs #186/#187

# Codex-native review producer

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Skill and catalog | `cc-skills/catalog.yaml`, Codex review-coordinator source | ~170 | 170 |
| Validation and docs | Gate A fixture/script and invocation docs | ~120 | 60 |
| **Total** | | | **~230** |

Band: **ideal**.

## Goal

Make `/review-coordinator` a genuine Codex-native catalog projection that produces
the landed `ReviewFindingsV1` contract for an exact PR head and hands it to Ship,
without copying Claude lifecycle prose or implementing another parser.

## Behavior

- Convert only `review-coordinator` from `portable-copy` to `target-adapted`.
- Preserve the current Claude source byte-for-byte.
- Add a repository-owned Codex source that uses connected GitHub access when
  available and invokes `reviewfindings github` for pagination, exact-head
  filtering, schema validation, and atomic output.
- Resolve the canonical `cc-skills` source revision at invocation time, refuse
  when the relevant catalog/skill source is dirty or the revision is unknown,
  and pass that value through `reviewfindings github --catalog-revision` into
  the typed producer provenance. Do not infer it later in Ship.
- Hand the artifact path to `ship driver address`; keep Ship authoritative for
  duplicate consumption, cycle capacity, and address-time head validation.
- Surface requested, completed, and missing reviewers. Silence is never clean.
- Never invoke Claude, mint grants, merge, or bypass Gate.
- Repair the pre-existing malformed `dev-workbench` YAML description quoting
  only as required to make catalog validation runnable; do not otherwise edit it.

## Acceptance

- A manifest dry-run/status chooses distinct Claude and Codex sources.
- A temporary Codex home discovers the native skill.
- The Claude projection is unchanged.
- Fixture validation pins the exact producer and Ship handoff commands.
- The generated artifact carries the catalog revision used by the fresh task.
- Stale-head, malformed, and empty-unsourced cases stop before Ship dispatch.

## Test plan

- Run `skill-sync status` and `sync --dry-run` through the repository version of
  skill-sync against temporary homes.
- Run the Gate A catalog script and focused fixture assertions.
- Verify the documented fresh-task PowerShell invocation.

## Non-goals

Gate judgment, Ship parser changes, receipt schema, or a live merge.
