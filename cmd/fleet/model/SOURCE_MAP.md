# Source map

The original model uses Go as an abstraction anchor. The crash extension adds
a scoped executable replay; neither establishes general implementation refinement.
Paths are relative to `cmd/fleet/internal/fleet/`.

## Crash/replacement extension

| Anchor | Model/test mapping |
|---|---|
| `policy.go` `CheckLease` | `claim` and `replace` call the real decision and persistence path in the process replay. Dead branches transfer; dead resources refuse. |
| `lease.go` `SessionAlive`, `policy.go` `Liveness` | `parentAlive` is observed from a synthetic harness record naming a real helper PID, which is killed and reaped by `crashParent`. The session record is retained. |
| `policy.go` `CheckLease` lock lifetime | The key lock ends when admission returns. It is not held over `spawnChild`, `childWrite`, or `replacementWrite`. |
| `crash_replacement_unix_test.go` | Replays the exported branch actions with the real lease store and process tree. `childAlive` is observed by PID; file bytes confirm both effects. Resource refusal is a control using the same attempted schedule. |
| `../../model/model/crash_replacement.qnt` `QuiescentBranch` | No production counterpart. Assumes a quiescence oracle before handing ownership to B. |

## Original lease model

| Anchor | Observed behavior | Model abstraction |
|---|---|---|
| `policy.go` `CheckLease` | The holder's own write needs no lock. Otherwise, under `KeyLock`: free is taken; malformed is refused; a rival holder's liveness is read through `Liveness`, and "not known" refuses; alive refuses; dead takes over a branch and refuses a resource. | `write(s)`: one atomic step. `Unreadable` is `Liveness` returning not-known. Malformed is out of scope. |
| `policy.go` `HeldByOther` | The same ladder as a state: free, malformed, unknown, live, orphaned (resource), dead (branch). | The `match live(h)` in `write`, and `takeover`'s guard. |
| `lease.go` `AcquireLease`, `TakeLease`, `DropLease` | Every mutation is inside `KeyLock` on the key. | Atomicity of `write`, `release`, `takeover` is assumed; `UnlockedCheckMutant` drops it for `write`. |
| `lease.go` `SessionAlive`, `Liveness` | A harness pid that answers, or a recent event from a parent-unverified session; a record that cannot be read is "not known". | `Alive` / `Dead` / `Unreadable`. |
| `session.go` `ReleaseSessionState` | SessionEnd removes the session's leases. | `release(s)`. |
| `hook.go` `onPreTool` | A write is authorized only after the lease verdict; the record publish that follows may fail, and then a lease-bearing write is refused. | `inFlight(s)` is the authorized tool call; publication failure is not modeled separately from `Unreadable`. |
| `verbs/keys.go` `CmdTake --takeover` | A person confirms a dead holder's resource is quiet before it changes hands. | `takeover(s)`, and the `Silent*` facts a mutant emits when no person was in the loop. |
