# An agent that controls work

Start with one coordinator and one worker. The coordinator uses judgment to reuse a worker's
context, queue related work, or start someone else. Workers return artifacts; the coordinator
checks that they combine into the requested outcome.

This experiment supplies the missing **job lifecycle** around those decisions: a durable local
queue, worker registry, fenced attempts and transactional delivery intents. The routing brain is
an actual agent using the CLI, not a keyword classifier. The [coordinator](cards/coordinator.md)
and [worker](cards/worker.md) cards are editable guidance.

## Try it

Python 3, standard library only. From this directory:

```sh
python3 controller.py --db /tmp/work-controller.db init
python3 controller.py --db /tmp/work-controller.db register builder 'Owns parser and its tests'
python3 controller.py --db /tmp/work-controller.db submit parser 'Implement parser with focused tests'
python3 controller.py --db /tmp/work-controller.db snapshot
python3 controller.py --db /tmp/work-controller.db decide parser --worker builder --reason 'Existing context; independent outcome'
python3 controller.py --db /tmp/work-controller.db claim parser --worker builder
```

The worker uses the returned token to report a result. The coordinator uses that same token to
accept it with evidence after inspecting it. Use `--help` for the remaining commands. `--spawn`
records a launch intent; it does not start or pay for a process. An external coordinator executes
the intent with its native tools, verifies the recipient, then marks the outbox event delivered.
This adapter remains agent-operated: there is no autonomous daemon or model API integration.

## What already exists

| Existing capability | Use it for | This experiment adds |
|---|---|---|
| Fleet `request` / `status` | Retry-safe branch assignment and observed activity | Claim, returned result and explicit acceptance lifecycle |
| Fleet `send` / `mail` / `ack` | Durable worker communication | Job assignment and delivery intent committed together |
| Native sessions / Fleet runtime | Start, observe and stop actual workers | Agent-authored routing reasons; no new runtime |
| Worktrees / Rooms | Execution isolation | Nothing; this store is local, not a shared guest filesystem |

The experimental registry is not synchronized with Fleet. Use it only for the experiment's
workers. Migration into Fleet should extend its existing records and mail, not leave two sources
of truth. No production command or hooks changed.

## Guarantees and limits

SQLite transactions serialize claims, assignment changes and outbox writes. A worker claims at
most one job at a time; other assigned jobs can wait. Reassignment invalidates the previous
attempt, so a delayed result cannot be accepted. Durable outbox delivery is at least once.
Message delivery, worker acknowledgement, execution and acceptance are different events.

A stale token cannot prevent an old process editing files or making external writes. There is
no automatic timeout reclaim, OS isolation, distributed lease, cost controller, or exactly-once
process launch. Acceptance evidence is an authored assertion, not an automatic verifier. Jobs
are scoped to one local SQLite store; do not share this file over a network filesystem or copy
it into multiple Rooms and call that a distributed system.

## Experiments

The [Import desk run](import-lab/RESULTS.md) builds a real browser/API app with cheaper models,
introduces operating pressure, replaces workers, and compares against native coordination.

Run `python3 -m unittest discover -s . -p 'test_*.py' -v` for mechanism checks.
See [RUNS.md](RUNS.md) for local, live-agent and Rooms drills. Run evidence belongs under `runs/`;
never commit databases, credentials or private task contents. A deterministic script can test
state transitions, but cannot establish that the coordinator's staffing decisions are good.

## Recover an uncertain operation

After losing a `claim` response, read `snapshot`: the job's worker, running state and token
recover the claim. A second `claim` refuses; it does not allocate a second attempt. After losing
a routing response, inspect the recorded decision and outbox before retrying. Identical result
reports and acceptance records replay safely; changed payloads refuse.

Pending events can become stale after reassignment. Before delivering an assignment or launch
intent, reconcile it with the current job owner and actual runtime registry. A `delivered` stamp
means transport delivery only. It does not establish acceptance by the worker. This prototype
has no authentication boundary: anyone with access to the DB can act as coordinator. Do not
expose this CLI as a multi-tenant service.
