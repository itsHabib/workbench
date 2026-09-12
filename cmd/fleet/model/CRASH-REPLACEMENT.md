# Session death does not establish child quiescence

## Result

The branch lease can change hands after a harness dies while a child it already
started remains able to write. The six-step Quint counterexample reproduces
against Fleet's real `CheckLease`, `Liveness`, key lock, and on-disk lease store
with an actual parent process killed and reaped while its child survives.

The child writes `A` after the replacement writes `B` to the same temporary
file. The resource-key control runs the same attempted schedule and refuses
replacement, retaining A's lease and producing only A's effect.

This is a demonstrated limit of branch ownership, not a newly introduced
behavior or a claim that two sessions simultaneously hold the lease record.
The existing lease model clears in-flight work on session death; this experiment
removes that assumption. Production behavior is unchanged by this experiment.

## Reproduce

From the repository root, using the existing Quint 0.32.x / Java / jq tools:

```sh
(cd cmd/fleet/model && ./judge.sh)
go test -race ./cmd/fleet/internal/fleet -run '^TestCrashReplacementModelTrace$' -count=1 -v
```

The judge typechecks the model, checks `CrashResource` and the hypothetical
`QuiescentBranch` with TLC using the 12-step configuration, and requires Apalache
to find the branch counterexample within six transitions. It normalizes and
compares that trace with `artifacts/crash-replacement.trace.json`. The Go test
consumes that exact fixture; it runs in ordinary repository CI on Linux without
requiring Quint. Regenerating/checking the models remains a local judge step.
The Go process test is Unix-only; Windows runtime behavior is unverified.

The original discovery used unrestricted `step`. Fixture regeneration uses
`witnessStep`, which selects that fixed schedule through the same guarded
transitions, avoiding solver-dependent choice among equivalent counterexamples.
Positive safety checks still use unrestricted `step`.

Green here means **the unsafe branch behavior was reproduced**, and the resource
control refused replacement. If branch recovery is hardened later, replace this
negative-result expectation with the new contract and regenerate the model
trace; do not keep unsafe behavior merely to satisfy this test.

## The witness

| Step | Operation | Observation |
|---|---|---|
| 1 | A claims | Fleet records A as the holder. |
| 2 | A starts a child | The child reports its PID and A's actual parent PID, then waits. |
| 3 | Kill A | The harness process is killed and reaped; Fleet observes known death. |
| 4 | B requests the key | A branch is transferred; a resource is refused. |
| 5 | B writes if admitted | Branch fixture contains `B`. Resource fixture has no B effect. |
| 6 | A's child resumes | Child appends `A` while B still owns the branch. |

The test controls ordering through a local socket, not timing sleeps. Every file
is under a test temporary directory. The helpers have timeouts, and closing the
socket makes the surviving child exit. On Linux the replay runs alone in a
subreaper process that adopts both orphaned test children and explicitly waits
for them; it asserts that both were reaped and no children remain. This does
not depend on a container's PID 1. Other Unix hosts rely on their system init
for orphan reaping. It does not use installed Fleet state,
kill an agent session, run Git writes, or touch a real resource.

The subreaper cleanup was exercised in ten consecutive runs with the test binary
as PID 1 in an Alpine 3.22 container, networking disabled and a 64-PID limit.
Every run asserted two adopted children reaped and `wait4` reporting no children
remaining. This is Linux test-harness evidence, not Fleet process containment.

## What the models say

- `CrashBranch` models the current dead-parent branch takeover decision and an
  independently surviving child. `noConflictingEffects` fails in six steps.
- `CrashResource` retains the resource for explicit recovery. `noStaleChildWrite`
  and `resourceRetained` hold in the checked finite model. Explicit takeover is
  deliberately outside this experiment.
- `QuiescentBranch` refuses replacement until the old child exits.
  `noStaleChildWrite` holds in the checked finite model. Its quiescence check is
  an **assumed oracle**, not an implemented Fleet capability or a liveness proof.

Each model has one key, two session identities, one child, and no parent restart,
PID reuse, delayed kernel I/O, external services, filesystem failures, or revoke.
The file effect is modeled as atomic. The process replay uses a synthetic
harness record naming the real helper PID and direct Fleet policy calls. It
does not exercise a real Claude/Codex harness, hook parsing, arbitrary shell
commands, or all schedules. The resource replay projects the branch's attempted
schedule into refusal; it is not a separately exported resource trace.

## Consequence for Fleet

The enforceable current statement is: **a lease records one owner and gates
recognized future tool admissions; a dead holder's already started descendants
may still have effects.** A stop flag or a lease epoch checked only at admission
does not stop an operation that already passed that check.

The next enforcement experiment should demonstrate stop-and-wait for the whole
execution scope before replacement, or fencing checked by the resource at the
actual effect. A fencing token carried only by Fleet cannot prevent an ordinary
unmediated child from writing a file. Until such an experiment is chosen, keep
the narrower guarantee explicit and retain the existing conservative treatment
of scarce resources. This change introduces no process supervisor or new
takeover policy.
