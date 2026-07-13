**Status**: draft
**Owner**: @codex:control-room
**Date**: 2026-07-13
**Related**: dossier task `control-room-adapter-ship` (id: `tsk_01KXDPT2G8G86R70NYXMHKNP7V`), [`../spec.md`](../spec.md), Ship contract PRs #193–#195

# Control Room Ship workflow and driver adapter — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Production source | `cmd/controlroom/internal/adapters/ship/*.go` | ~100 | ~100 |
| Tests and fixtures | same package plus Phase 1 fixtures | ~120 | ~60 |
| **Total** | | **~220** | **~160** |

Band: **ideal** per the repository PR-sizing convention.

## Goal

Read Ship-owned workflow and driver truth through its frozen JSON CLI contracts without reading Ship SQLite, interpreting private artifacts, or invoking mutation verbs.

## Behavior / fix

- Add the isolated adapter package `cmd/controlroom/internal/adapters/ship`; it must not change the Phase 2 model or any other adapter directory.
- The package may import but must not modify `cmd/controlroom/internal/model`. Export a source-local `Result` containing `[]model.Run` plus `model.SourceReceipt`; if a required shared model type is absent, stop and escalate rather than extending the locked package.
- Execute only enriched `ship list --json`, targeted `ship status <workflow-id> --json`, `ship driver list --json`, and point `ship driver status <driver-id> --json`. Call workflow status at most once for a valid inventory ID when its list row omits any normalized display fact: producer status/timestamps/phases, requested or actual runtime/provider/model, duration, failure, or evidence availability/refs. Call driver status at most once for a valid driver ID when its list row omits producer status/timestamps, repository/project/phase, or the batch/stream/attempt facts needed to normalize child runs. Complete inventory rows cause no point call; invalid/missing IDs cannot be repaired by detail lookup. All calls share the one source deadline.
- Normalize local, cloud, and rooms records into the existing run shapes, keeping producer status separate from Control Room policy. Preserve owner-reported requested/actual runtime, provider, model, phase, failure, duration, task/spec identity, and evidence availability. Unknown or absent fields remain explicit `Availability` states.
- Treat additive JSON fields as compatible. A wholly malformed inventory fails the Ship source; a malformed/missing detail record keeps the inventory entity with qualified unknown fields and a degraded receipt.
- Use injected command/context seams, the configured executable/argv/timeout, and sanitized typed error codes. Never expose raw stderr, usernames, absolute operator paths, or credential-like values.
- Prohibit `tick`, `dispatch`, direct database access, `result.json` parsing, and fallback to any owner-private store.

## Acceptance

Healthy, empty, additive-field, malformed-inventory, partial-detail, timeout, nonzero-exit, local/cloud/rooms, and missing-evidence fixtures produce deterministic rows and honest receipts. Stable ordering does not depend on subprocess return order, and no mutation-capable command is representable through the adapter.

## Test plan

Use fake executables and the Phase 1 Ship fixtures. Assert exact argv, context cancellation, status/detail call bounds, stderr sanitization/truncation, additive tolerance, stable ordering, availability mapping, and absence of mutation/store fallback tokens. Run `gofmt -l .`, `go vet ./...`, `golangci-lint run ./...`, `go test ./...`, `go build ./...`, and `git diff --check`.

## Non-goals

Cross-source joins, refresh orchestration, stale-payload retention, browser work, owner-contract changes, or shared adapter helpers outside this source directory.
