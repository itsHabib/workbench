# Claims ledger

## Proved about the bounded models

- TLC exhausts the reference graph to twelve steps for a `Branch` key and for
  a `Resource` key and finds no violation of `exclusion`, `writerHolds`,
  `evidenceNotDeath`, or `noSilentResourceTakeover`.
- Apalache finds a bounded counterexample in which a holder's record becomes
  unreadable and a rival's write is authorized while the holder is alive and
  its own write is still in flight (`UnreadableIsDeadMutant`).
- Apalache finds a bounded counterexample in which a dead holder's resource
  changes hands on a rival's write with no takeover recorded
  (`SilentResourceTakeoverMutant`).
- Apalache finds a bounded counterexample in which two sessions both observe a
  free key and both commit, leaving two writes in flight
  (`UnlockedCheckMutant`).

## Replayed executable fixtures

`./judge.sh` regenerates the three traces and compares each with the
checked-in JSON under `artifacts/` byte-for-byte, then asserts the salient
failure is still in the last state of each.

## Replayed against the binary

The first counterexample is the shape of the review finding fixed at workbench
#282 (`CheckLease` treating an unreadable record as death). The suite's
scenario "a holder whose session record cannot be read is not dead: the rival
is refused and the lease stands" in `../testdata/test.sh` drives the Go binary
through the same steps: a holder whose record is unreadable, a rival's write,
and the write refused with the reason naming the unreadable record. The second is the suite's `--takeover`
scenarios; the third is the `x-hold` race scenarios that block a rival inside
the lock.

## Mapped, not proved about source

The model does not execute Go, read a real store, exercise `KeyLock`, model
malformed records or migration, or establish refinement between the Quint and
the Go. `SOURCE_MAP.md` ties each transition to the function it abstracts.

## Remaining unproved

- The lock's kernel release on process death (a Windows and Linux hardware
  property; `~/verify-windows.md` names the probe).
- Publication failure of the session record after the verdict, which the Go
  refuses on a lease-bearing write; it is folded into `Unreadable` here.
- Anything about more than two sessions or more than one key.
