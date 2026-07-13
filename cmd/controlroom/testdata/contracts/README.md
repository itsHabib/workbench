# Control Room contract fixtures

Sanitized, producer-shaped fixtures for Phase 1 source contracts. All timestamps
and IDs anchor to the TDD demo clock: **2026-07-13T12:00:00.000Z**.

Phase 2 adapters and the presentation model consume these shapes; fixtures
preserve producer envelopes — normalization belongs to later phases.

## Demo clock

| Field | Value |
|---|---|
| Anchor instant | `2026-07-13T12:00:00.000Z` |
| Workflow run ID | `wf_demo_01` |
| Driver run ID | `drv_demo_01` |
| Task ID | `tsk_demo_01` |
| Linkage doc path | `docs/features/example-feature/spec.md` |

## Source inventory

| Source | Directory | Healthy | Degraded / unavailable | Notes |
|---|---|---|---|---|
| Ship workflows | `ship/` | `workflow-list-healthy.json`, `workflow-status-healthy.json` | `workflow-list-empty.json`, `workflow-list-malformed.json`, `source-unavailable.json` | PR #194 (`e76a8dd`); no SQLite/manifest/artifact fixtures |
| Ship drivers | `ship/` | `driver-list-healthy.json` | `source-unavailable.json` | PR #193 (`66d1f49`); `specPath` linkage required |
| Dossier MCP | `dossier/` | `task-get-healthy.json`, `task-list-healthy.json` | `session-failure.json` | JSON-RPC result/error framing over stdio MCP |
| GitHub | `github/` | `graphql-inventory-healthy.json`, `pr-detail-complete.json` | `receipt-inventory-truncated.json`, `pr-detail-truncated.json`, `source-unavailable.json` | GraphQL producer + adapter receipts; four-page/200-PR cap |
| Tracelens | `tracelens/` | `analysis-findings.json` | `analysis-unavailable-telemetry.json`, `source-unavailable.json` | `tracelens ship -json <run-ref>` shape |
| Tool health | `toolhealth/` | `accumulated-friction.txt` | `live-incident.txt`, `source-unavailable.json` | Provisional tolerant text surface until `toolhealth -json` |
| Tower (optional) | `tower/` | `ls-available.json` | `source-unavailable.json` | Supplemental; missing Tower is never a startup failure |

## Provenance

Exact producer commands used as shape references when authoring fixtures.
Tests do not execute these commands.

| Fixture group | Producer command | Owner repo | PR / SHA | Version |
|---|---|---|---|---|
| Ship workflow list/status | `ship list --json`; `ship status wf_demo_01 --json` | Ship repository | PR #194 / `e76a8dd40514e8ac0ecc483b7bbe64085aecc6ec` | merged 2026-07-13 |
| Ship driver list | `ship driver list --json` | Ship repository | PR #193 / `66d1f49bd526632d03873ad887243eb3199ae3da` | merged 2026-07-13 |
| Dossier task reads | `dossier serve --corpus <path>` then MCP `tools/call` for `task.get`, `task.list` | Dossier repository | main @ authoring time | stdio MCP JSON-RPC |
| GitHub PR inventory | `gh api graphql -f query=@inventory.graphql` with scoped search | `cli/cli` (gh) | gh >= 2.90.0 | GraphQL search API |
| Tracelens analysis | `tracelens ship -json wf_demo_01` | Tracelens repository | main @ authoring time | verdict + telemetry availability |
| Tool health board | `toolhealth` (text board, no `-json` yet) | operator-local | n/a | provisional text contract |
| Tower worktrees | `tower ls --json --no-reconcile` | Tower repository | main @ authoring time | flat JSON array |

Ship's owner contract intentionally uses one-based `batchIndex` values and
zero-based `streamIndex` values; the mixed convention in the driver fixture is
producer fidelity, not fixture normalization.

GitHub's `graphql-inventory-healthy.json` is the raw GraphQL producer envelope.
The `pr-detail-*.json` fixtures are adapter-augmented shapes: Control Room adds
`detail_state`; `gh` does not emit that field.

Tracelens intentionally emits `subject.number: 0` when a trace has no pull
request context; its owner contract uses a non-nullable integer to remain
byte-compatible with Gate. Fixture links use neutral HTTPS values because raw
local `file://` report locations are never browser-facing Control Room links.

## Privacy

Fixtures contain no real operator usernames, credentials, absolute home paths,
raw prompts, or sensitive traces. Relative linkage paths (`docPath`, `specPath`)
use neutral values such as `docs/features/example-feature/spec.md`.

Validation: `go test ./cmd/controlroom/...` (`fixtures_test.go`).

Configuration contract: [`docs/features/portfolio-control-room/source-config.md`](../../../docs/features/portfolio-control-room/source-config.md).
