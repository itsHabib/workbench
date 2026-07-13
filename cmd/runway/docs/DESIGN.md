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
contiguous sequence numbers and writes the journal. Concurrent backend Wait
and controller interrupt paths funnel every emit through one mutex so seq and
NDJSON stay coherent.

## One-writer rule

While the foreground controller lives it is the sole canonical event/result
writer for its run. `watch` / `logs` / `result` are read-only over durable
state. `cancel` writes only `private/cancel.request` and best-effort wakes the
verified controller identity — it never appends lifecycle events. The marker
is authoritative; on Windows the wake is a no-op, so cancel latency equals the
controller's marker poll interval (`cancelPollInterval`).

Terminal transition is exclusive in-process: `result.json` is written via
temp file + `Sync` + atomic rename, then `run_terminal` is appended. An
existing result is immutable; a second writer must not clobber it. Every
receipt passes `execution.ValidateResult` before rename. If the process dies
between those writes, `watch --follow` treats `result.json` presence as a
terminal fallback when the journal is still non-terminal (section-8 repairable
partial state; writer order stays result-first).

## Absolute deadline and preparation

`policy.deadline_ms` is absolute and armed before preparation (FR9). Bundle
materialization (including `git clone`/`checkout`) runs under the same
deadline and cancel-marker select. On interrupt during preparation the
controller cancels Materialize's context — `CommandContext` kills in-flight
git, the input-copy loop stops between files — then joins the materialize
goroutine before emitting the `timed_out`/`cancelled` receipt with
`terminal_phase=preparation`. No writer remains in the run dir (zero-orphan).

## Process-start identity

Cancel verifies recorded controller PID plus process-start identity before
signaling. Linux uses `/proc/<pid>/stat` starttime; Windows uses process
creation FILETIME. Other Unix GOOS (darwin, freebsd, …) compile but return an
explicit runtime error — process-start identity is unsupported/untested there,
so cancel fails closed rather than trusting PID alone.

## What PR 3 adds

Controller death leaves an open history in this PR — nothing repairs it yet.
PR 3 adds writer-claim acquisition, `reconcile`, and the Flow F
`controller_lost` receipt path.
