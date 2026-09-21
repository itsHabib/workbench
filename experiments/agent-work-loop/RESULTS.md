# Jobs kept the work. Checks caught the mistakes.

We connected a real Haiku coordinator and two workers to Fleet's durable jobs.
They built a Python/SQLite document importer while new requirements arrived, a worker
was interrupted and the coordinator was replaced. The first completion failed an
independent check. Repair feedback produced a better application. This was a
supervised integration pilot, not evidence that a team beats one capable session.

| Result | Observed |
|---|---|
| Model | Haiku 4.5 for coordinator and workers |
| Work | Four accepted jobs; interrupted backend job took two attempts |
| First completion | $2.16 reported usage; independent API check failed |
| Final artifact | API and browser checks passed; $3.67 across 20 provider turns |
| Supervision | Seven recorded steering/harness interventions, besides planned faults |

The [receipt and replay](runs/haiku-01/README.md) preserve costs, failed checks,
the final app and a patch that reproduces the first broken completion.

**What the machinery earned**

- **An interrupted worker kept its work.** The bridge renewed its job independently
  of the watcher. After a terminal cancellation and lease expiry, another attempt
  continued in the same checkout. A separate deterministic drill killed the watcher,
  waited beyond the lease TTL and proved the still-running worker retained its claim.
- **A result was not automatically accepted.** Worker reports carried commits and
  artifacts; the coordinator integrated them. A lost lease could not report a result,
  and the old claim token could not complete a reclaimed attempt.
- **Caller-owned checks could reject the whole result.** The launcher retained a
  failed completion and placed concrete failure output directly in a fresh wake.
  Process uncertainty remained visible instead of releasing a possibly live checkout.

**What the agents got wrong**

- The first "done" app returned raw payloads instead of documents, mishandled retries
  and accepted malformed CSV. The team's own tests passed. More passing tests would
  not have helped if they kept testing the wrong contract.
- A resumed coordinator trusted its stale plan and skipped new rejection feedback.
  Putting the rejection in the wake itself was more dependable than another file to read.
- The API preserved quoted multiline CSV, but the browser split it into corrupt rows.
  The first response added backend tests. A later pass located the browser parser.
  The check now names the failing browser operation explicitly.
- After repairing, the coordinator sometimes said "ready for acceptance" without
  writing the completion artifact. A clearer reminder was still unnecessary overhead.
  With an explicit check, settled accepted jobs now go directly to verification.

**What changed in Fleet because of this run**

The bridge no longer tells an agent to abandon its entire turn whenever a command
needs approval. The denial remains; other authorized work can continue. The launcher
waits for the provider's terminal result after DONE, preserves existing permissions,
checks its watcher ownership before stopping anything and observes the remaining
budget before returning failed work. Verification records the actual source contents,
including dirty work, rather than attributing everything to HEAD.

**What this does not establish**

This was one changing local run, with explicit steering and harness fixes. There was
no matched native-session baseline, no general intelligence uplift measurement, no
Rooms deployment and no proof of arbitrary escaped-child or external-side-effect
recovery. File-backed claims here require a shared local store. The dollar figure in
the receipt is provider-reported usage, excluding engineering, evaluation and local compute.
The run changed while we learned; it is not a fixed-protocol benchmark. Subsequent
checks validate the resulting implementation, not a claim of unattended success.

**The next useful comparison**

Run the same changing task with native sessions plus one task file, then with jobs.
Keep model, starting code, requests, fault schedule, checks and cost accounting fixed.
Measure accepted behavior, time, cost and human rescues. Add targeted boost as a
separate arm. Keep the queue only where restart/retry evidence pays for its complexity.
Rooms comes after that: replace the local storage assumption and test real process
death and isolation, rather than treating a local pass as cloud readiness.

See the [launcher guide](README.md) for commands.
