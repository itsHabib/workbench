**Status**: accepted
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
- The package may import but must not modify `cmd/controlroom/internal/model`. Export a source-local `Result` containing `[]model.Task` plus `model.SourceReceipt`; if a required shared model type is absent, stop and escalate rather than extending the locked package.
- Expose a lifecycle-safe adapter seam: `serve` can retain one handshaken child across collections; one-shot snapshot collection can start and close one child. Serialize calls unless the protocol owner explicitly guarantees multiplexing.
- EOF, child exit, handshake timeout, malformed JSON-RPC, mismatched response ID, and call errors fail only Dossier and invalidate the child. Cleanup closes stdin, waits up to one second for natural exit, then kills and waits/reaps the process; all timings and process operations are injectable. Replacement is attempted only on the next eligible refresh, never again in the same refresh cycle.
- On each refresh, make at most one start attempt, and let the whole refresh cycle contribute at most one failure to the consecutive count regardless of which or how many lifecycle stages fail. After three consecutive start, handshake, or first-call failure cycles, pause automatic probes for five minutes. While open, automatic calls return immediately with `state=unavailable` and `error_code=breaker_open` without starting a child.
- One manual refresh may begin one half-open probe. A mutex/singleflight guard makes every concurrent manual caller join that same probe and receive the same result; no second child starts. Automatic callers remain suppressed while it is in flight. Success resets the breaker; failure counts as one failed cycle and reopens it for five minutes. Inject the clock and process factory for deterministic tests.
- Normalize exact tasks, dependencies, blockers, phases, assignees, timestamps, and artifact refs into the locked model. Ignore additive fields; never parse Dossier markdown or its `.dossier` store as fallback.
- Sanitize all process/protocol failures into typed receipts and never place raw stderr or JSON-RPC bodies into the snapshot.

## Acceptance

Healthy, empty, additive, EOF, malformed response, child exit, handshake/call timeout, restart, breaker-open, automatic suppression, concurrent manual half-open, and recovery cases produce deterministic rows and isolated receipts. The child is reaped on cancellation/shutdown, one refresh increments the breaker at most once, calls cannot cross-correlate, and success resets the full failure counter.

## Test plan

Use an injected scripted MCP child. Cover handshake ordering, IDs, every allowed tool, structured/text results, cancellation, cleanup, one-attempt refresh law, the three-failure/five-minute breaker, manual half-open concurrency, task/artifact fidelity, and sanitization. Run the full Go gates.

## Non-goals

Composition, stale-claim cross-source evaluation, HTTP scheduling, direct store reads, Dossier mutations, or shared adapter helpers outside this source directory.
