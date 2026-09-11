# The lease protocol, bounded and machine-checked

The [crash/replacement experiment](CRASH-REPLACEMENT.md) extends this work with
independent parent/child lifetimes and replays a model-generated counterexample
against Fleet's real Go lease code and subprocesses. It demonstrates that branch
takeover after parent death does not stop an already running child. A passing
replay preserves this negative result; it is not evidence of process containment.

## Original lease model

A Quint model of `CheckLease` (`../internal/fleet/policy.go`) and the lease
mutations around it: two sessions, one key, the holder's liveness as a rival
reads it, and what a write is allowed to do. It exists because the switch
from the Python hook to this binary is the moment the Go code starts refusing
writes on real branches, and the one review finding that mattered most was
about evidence: an unreadable session record was being treated as a dead
holder.

```sh
./judge.sh
```

The judge typechecks every module, exhausts the reference graph to twelve
steps with TLC for both key kinds, then reproduces four counterexamples with
Apalache (the three lease mutants below plus crash/replacement). It compares
each normalized trace byte-for-byte with the checked-in one under `artifacts/`.

## What is modeled

- A key of kind `Branch` or `Resource`. A dead holder's branch is taken over on
  the next write; a dead holder's resource is orphaned and needs an explicit
  `takeover`, because the machine it drives may still be running.
- Two sessions `A` and `B`, each `Alive`, `Dead`, or `Unreadable` — the last is
  the session's record existing but not readable by a rival.
- `write(s)`: the atomic decision under the key's lock. Free: take. Mine:
  proceed. Held by a rival: `Alive` and `Unreadable` both refuse; `Dead` takes
  over a branch and refuses a resource.
- `finish`, `release`, `crash` (the holder's in-flight write vanishes with the
  process, the lease file stays), `obscure` / `reveal` (the record's
  readability), `takeover` (a person confirmed the resource is quiet).

## What must hold

| invariant | claim |
|---|---|
| `exclusion` | two writes are never in flight at once |
| `writerHolds` | a write in flight belongs to the session that holds the key |
| `evidenceNotDeath` | while the holder is not known dead, no rival is authorized |
| `noSilentResourceTakeover` | a resource never changes hands after a death without a takeover |

## The mutants

| mutant | what it changes | violates |
|---|---|---|
| `UnreadableIsDeadMutant` | an unreadable record is treated as a dead holder | `evidenceNotDeath` |
| `SilentResourceTakeoverMutant` | a dead holder's resource is taken on write, like a branch | `noSilentResourceTakeover` |
| `UnlockedCheckMutant` | the check and the write are two steps with no lock between | `exclusion` |

Each is a plausible alternative, not a claim about the shipped Go. The first is
the shape the code had before the review at workbench #282.

## What is not modeled

In the original lease model: malformed lease files, migration, the codex
adapter's rollback, the stop flag,
revoke, and `KeyLock` itself: the lock is a model assumption (atomicity of
`write`), and `UnlockedCheckMutant` is what its absence looks like. The Go is an
anchor for the abstraction, not a conformance oracle; see `SOURCE_MAP.md`.
