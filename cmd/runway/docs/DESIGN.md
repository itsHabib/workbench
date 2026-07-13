# runway — design notes (PR 2)

**Status:** local lifecycle policy over PR 1 mechanisms.
**Related:** [execution-runtime TDD](../../../docs/features/execution-runtime/spec.md).

## Policy vs mechanism

| Layer | Owns | Must not own |
| --- | --- | --- |
| `internal/controller` | phase transitions, absolute deadline, cancel intent, collection truth (D7), cleanup ordering, atomic `result.json` + `run_terminal`, CLI durable reads | process-group kill details, path expansion, journal append format |
| `internal/backend/*` | start / wait / cancel / collect / cleanup mechanisms | status/reason/phase decisions, seq assignment, result writing |
| `internal/journal`, `state`, `bundle`, `expand` | durable layout + append-only events + admission materialization | lifecycle policy |

Backends propose observations through `Emit`. Only the controller assigns
contiguous sequence numbers and writes the journal.

## One-writer rule

While the foreground controller lives it is the sole canonical event/result
writer for its run. `watch` / `logs` / `result` are read-only over durable
state. `cancel` writes only `private/cancel.request` and signals the verified
controller identity — it never appends lifecycle events.

Terminal transition is exclusive in-process: `result.json` is written via
temp file + `Sync` + atomic rename, then `run_terminal` is appended. An
existing result is immutable; a second writer must not clobber it. Every
receipt passes `execution.ValidateResult` before rename.

## What PR 3 adds

Controller death leaves an open history in this PR — nothing repairs it yet.
PR 3 adds writer-claim acquisition, `reconcile`, and the Flow F
`controller_lost` receipt path.
