# What helped a stuck agent team?

**A stronger lead helped in one rescue. Better instructions and checks also
let the cheaper model finish alone. A mandatory challenger did not earn its
place.** These are observations from a small local pilot, not a team-performance
benchmark. The useful product so far is a repeatable way to test interventions.

## Repair a real executable service

Each arm repaired the same deliberately unfinished webhook service. The checker
started real HTTP recipients, restarted the service, and checked delivery,
idempotency, retries, persistence and metrics. Roles used fresh native model
invocations with authored memory; turns were sequential. Workers returned code;
the harness ran it. This was not a parallel repository team with native tools.

One trial per arm, eight calls maximum, 120 seconds per call, low reasoning
effort. Calls include routing and review. Tokens below are input plus output,
including cached input; they are **not dollars**. Native startup/shutdown time
is included in model seconds. All trials used zero human answers or code fixes.

| Phase 1 policy | Original checker + explicit finish | Calls | Known tokens | Model seconds |
|---|---|---:|---:|---:|
| Astra solo | Accepted | 2 | 63,674 | 73.7 |
| Luna lead + Luna workers | Accepted | 5 | 149,545 | 129.1 |
| Luna solo | Accepted | 7 | 222,055 | 331.6 |
| Luna lead + Luna challenger + Luna workers | Budget exhausted | 8 | 239,783 | 188.2 |

All four final candidates passed the **original** checker. That checker missed
real defects. Preserve these grades; the later probes below qualify them.
Receipts: [original phase 1](runs/pilot-01/phase1.json).

Rechecking every candidate with the corrected checker left **only Luna lead's
phase-1 candidate fully green**. Astra returned HTML/501 for an unsupported
method; Luna solo dropped an invalid-input connection; the pair had that method
gap plus data loss. Astra's phase-2 candidate retained the method gap. These
are [post-hoc checks](runs/pilot-01/corrected-checks.json), not revised original
scores or extra repair opportunities.

## The review loop introduced a regression

The pair's first implementation passed. Its challenger found genuine invalid
input gaps, mixed them with extra hardening requirements, and the lead forwarded
the bundle. The repair required stored event payloads to be dictionaries, even
though the contract allowed **any JSON**. A valid scalar payload then caused
the entire stored state to be discarded on restart.

The original checker only exercised object payloads. Neither the challenger
nor the next reviewer caught the new data loss. They spent the remaining calls
discussing unsupported HTTP methods. [Independent post-hoc probes](runs/pilot-01/posthoc.json)
reproduce the regression; the [candidate code](runs/pilot-01/candidates/pair/service.py)
is retained unchanged. These probes were designed after the run and are not
fresh qualification cases.

Separately, Luna solo returned unchanged code four times after reaching a green
check. Our worker prompt overemphasized returning edits. That is a harness
confound, not evidence that the model inherently needs a manager.

## Reveal a late requirement

After each eligible arm explicitly completed phase 1, reveal replay support
without replenishing its eight-call budget. Keep the original frozen runtime.

| Policy | Total calls across both phases | Original phase 2 outcome |
|---|---:|---|
| Astra solo | 4 | Accepted |
| Luna lead + workers | 8 | Replay retry activation failed; budget exhausted |
| Luna solo | 8 | Terminal replay rejected; budget exhausted |
| Luna pair | 8 | Never completed phase 1; not advanced |

This tests how much budget the **whole policy** left for new work. It does not
give each policy equal additional attempts. [Original phase 2 receipts](runs/pilot-01/phase2.json)
retain all four rows.

## Rescue the observed failure

Freeze a new comparison using the pair's actual broken code and recorded team
history. Both arms get corrected checks for every JSON payload category, invalid
field types and JSON error responses. Both get clearer completion instructions
and feedback for unchanged edits. Five calls maximum; no mandatory challenger.
The [follow-up protocol](PROTOCOL.md#follow-up-fixed-after-pilot-01-before-rescue-calls)
was committed before either arm ran.

| Rescue policy | Corrected checks + explicit finish | Calls | Known tokens | Model seconds |
|---|---|---:|---:|---:|
| Luna solo, improved instructions | Accepted | 4 | 128,320 | 141.5 |
| Astra lead + Luna worker | Accepted | 3 | 100,451 | 81.9 |

Luna first fixed unsupported methods and declared completion while the payload
check still failed. The harness refused that declaration; Luna then repaired
the data loss. Astra identified both failures in its first assignment, Luna
fixed them together, and Astra finished. No human intervened in either run.
[Rescue receipts and actions](runs/rescue-01/results.json).

The stronger lead used fewer calls and tokens here. Its invoice cost is unknown,
and one instance cannot establish a reliable advantage. Both policies changed
from the initial pilot; this is not an isolated test of model strength.

## Lessons worth keeping

- **Give the lead a failure to resolve.** A precise assignment repaired both
  defects in one worker turn. The mandatory review loop consumed calls without
  protecting the result.
- **A finding is a hypothesis, not a requirement.** Tie repairs to the contract
  and a concrete counterexample. Bundled hardening introduced silent data loss.
- **Improve the cheapest baseline.** Clear completion instructions and feedback
  made solo recovery viable. Compare against that before buying more roles.
- **Verify the artifact, not the declaration.** The corrected checker blocked a
  false finish and drove the next repair. The first checker also had blind spots.
- **Keep failures and versions.** We could reproduce exactly where a green
  implementation regressed and rescue the same snapshot without inventing faults.

## What is built, and what remains

The local driver uses Fleet's durable claims and action receipts, records model
usage and role context, versions candidate files, handles late requirements,
and enforces call/time/stop limits. **35 mechanism tests pass**, including real
driver/checker process death, restart reconciliation, duplicate driver exclusion,
stale receipt rejection, source tampering and failed delivery controls. CI runs
these without paid model calls.

This proves a useful local test harness and one synthetic-service rescue. It
does not prove parallel team throughput, unattended safety, general intelligence,
equal-dollar superiority, or production webhook reliability. No Rooms resources
were used. The six experiments in the protocol are not all complete.

Next: use three independently observed stalls on different tasks, compare
improved solo with optional lead intervention, and include a fresh final checker.
Then test real concurrent workers and missing-context recovery. Keep Rooms last.
Use the [run guide](runs/README.md) to reproduce the checks or start another trial.
