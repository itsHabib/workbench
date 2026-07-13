# runway — design notes (PR 3)

**Status:** Gate B — writer claim + controller-loss reconcile.
**Related:** [execution-runtime TDD](../../../docs/features/execution-runtime/spec.md).

## Policy vs mechanism

| Layer | Owns | Must not own |
| --- | --- | --- |
| `internal/controller` | phase transitions, absolute deadline, cancel intent, collection truth (D7), cleanup ordering, atomic `result.json` + `run_terminal`, Flow F reconcile policy, CLI durable reads | process-group kill details, path expansion, journal append format, claim O_EXCL |
| `internal/claim` | atomic writer exclusivity (`O_CREATE\|O_EXCL`), takeover rename, process-start identity ticks | when to reconcile, which receipt to write |
| `internal/backend/*` | start / wait / cancel / collect / cleanup mechanisms, durable `backend.json` | status/reason/phase decisions, seq assignment, result writing |
| `internal/journal`, `state`, `bundle`, `expand` | durable layout + append-only events + admission materialization | lifecycle policy |

Backends propose observations through `Emit`. Only the controller assigns
contiguous sequence numbers and writes the journal. Concurrent backend Wait
and controller interrupt paths funnel every emit through one mutex so seq and
NDJSON stay coherent.

## One-writer rule

The foreground controller acquires an exclusive per-run writer claim
(`private/writer.claim`) before any durable mutation beyond the empty run
directory, and is the sole canonical event/result writer while it lives.
`watch` / `logs` / `result` are read-only over durable state. `cancel` writes
only `private/cancel.request` and best-effort wakes the verified controller
identity — it never appends lifecycle events.

`reconcile` may become writer only after proving the recorded controller
identity is absent or reused and atomically taking over the same claim.
Concurrent reconcilers: exactly one mutates; losers exit without touching the
journal.

Terminal transition is exclusive: `result.json` is written via temp file +
`Sync` + atomic rename, then `run_terminal` is appended. An existing result is
immutable. If the process dies between those writes, `reconcile` treats the
result as authoritative and appends only the missing terminal event.

## Writer claim primitive (TDD open question #4)

**Exclusivity** comes only from the filesystem: `os.OpenFile(...,
O_CREATE|O_EXCL, 0600)` — atomic on both Linux and Windows, stdlib-only, and
leaves no stuck lock when the holder crashes (death is detectable; takeover
recovers).

**Detection** of a live owner uses PID + process-start identity, never as the
lock itself:

- Linux: `/proc/<pid>/stat` starttime (field 22)
- Windows: process creation FILETIME via `syscall.GetProcessTimes` (no
  `golang.org/x/sys`)

**Takeover** (reconcile path) stays race-safe when the recorded owner is dead:
create `writer.claim.takeover.<nextGen>` with `O_EXCL`, then `rename` over
`writer.claim`. Two reconcilers may both observe death; exactly one wins the
`O_EXCL`. Never "verify dead, then write in place" (TOCTOU).

## Absolute deadline and preparation

`policy.deadline_ms` is absolute and armed before preparation (FR9). Bundle
materialization (including `git clone`/`checkout`) runs under the same
deadline and cancel-marker select. On interrupt during preparation the
controller cancels Materialize's context — `CommandContext` kills in-flight
git, the input-copy loop stops between files — then joins the materialize
goroutine before emitting the `timed_out`/`cancelled` receipt with
`terminal_phase=preparation`. No writer remains in the run dir (zero-orphan).

## Process-start identity

Cancel and reconcile verify recorded controller PID plus process-start
identity. The claim package owns the tick primitive; controller identity and
claim owner records share it. Other Unix GOOS (darwin, freebsd, …) compile
but return StartTicks 0 — LiveMatches / liveMatches fall back to pid
existence so a still-alive unverifiable owner cannot be stolen.

## Flow F — controller loss

`runway reconcile <run-id>`:

1. No-op if `result.json` and `run_terminal` already exist.
2. Refuse if the recorded controller identity is still live.
3. Atomically take over the writer claim; on loss, print existing owner/result
   and exit without mutation.
4. Best-effort backend cleanup from `private/backend.json` — probe only what
   is provable; kill the process group only when identity matches.
5. If `result.json` exists without `run_terminal`: append only the missing
   terminal event.
6. Otherwise: write `failed/terminal/controller_lost`, then `run_terminal`.
7. Uncertain backend liveness fails closed: status stays failed; the remaining
   allocation is named in `diagnostics[]`.
