**Status**: draft
**Owner**: @codex:control-room
**Date**: 2026-07-13
**Related**: dossier task `control-room-adapter-dossier` (id: `tsk_01KXDPT2H72M69NA57PV4FWB0W`), [`../spec.md`](../spec.md), Dossier MCP protocol

# Control Room long-lived Dossier MCP adapter — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---:|---:|
| Production source | `cmd/controlroom/internal/adapters/dossier/*.go` | ~160 | ~160 |
| Scripted MCP tests | same package | ~160 | ~80 |
| **Total** | | **~320** | **~240** |

Band: **stretch** per the repository PR-sizing convention.

## Goal

Read project, phase, task, and artifact truth through Dossier's stdio MCP owner interface, with a supervised child and exact breaker behavior rather than markdown parsing or process-per-call fan-out.

## Behavior / fix

- Add only `cmd/controlroom/internal/adapters/dossier`. Implement MCP `initialize`, `notifications/initialized`, request ID correlation, structured/text tool result decoding, and bounded calls to `project.list`, `project.overview`, `phase.list`, `task.list`, `task.get`, and `artifact.list`.
- Expose a lifecycle-safe adapter seam: `serve` can retain one handshaken child across collections; one-shot snapshot collection can start and close one child. Serialize calls unless the protocol owner explicitly guarantees multiplexing.
- EOF, child exit, handshake timeout, malformed JSON-RPC, mismatched response ID, and call errors fail only Dossier and terminate/replace the child safely.
- On each refresh, make at most one start attempt. After three consecutive start, handshake, or first-call failures, pause automatic probes for five minutes. One manual refresh may perform one half-open probe; success resets the breaker and failure reopens it. Inject the clock and process factory for deterministic tests.
- Normalize exact tasks, dependencies, blockers, phases, assignees, timestamps, and artifact refs into the locked model. Ignore additive fields; never parse Dossier markdown or its `.dossier` store as fallback.
- Sanitize all process/protocol failures into typed receipts and never place raw stderr or JSON-RPC bodies into the snapshot.

## Acceptance

Healthy, empty, additive, EOF, malformed response, child exit, handshake/call timeout, restart, breaker-open, automatic suppression, manual half-open, and recovery cases produce deterministic rows and isolated receipts. The child is reaped on cancellation/shutdown, calls cannot cross-correlate, and success resets the full failure counter.

## Test plan

Use an injected scripted MCP child. Cover handshake ordering, IDs, every allowed tool, structured/text results, cancellation, cleanup, one-attempt refresh law, the three-failure/five-minute breaker, manual half-open concurrency, task/artifact fidelity, and sanitization. Run the full Go gates.

## Non-goals

Composition, stale-claim cross-source evaluation, HTTP scheduling, direct store reads, Dossier mutations, or shared adapter helpers outside this source directory.
