# Make the controller earn its place

## Local mechanics

Inject duplicate submissions, simultaneous claims, coordinator restart, queued follow-up work,
reassignment and a late result from the old attempt. Preserve a CLI transcript. Fail the run if
an acknowledged job disappears, two claims succeed for one available attempt, the old token is
accepted, or acceptance precedes a result. These are correctness tests, not a staffing benchmark.

## Live coordinator + worker

Use these role cards on a useful repository change. While its worker builds, submit a related
follow-up and an independent request. Coordinator must record routing reasons before seeing the
results. Let the worker challenge a bad assignment. Introduce an integration conflict and require
a combined demonstration, then replace the coordinator using only persisted records.

Record tools, model identity when available, session IDs, costs if exposed, result SHAs and
operator interventions. Compare with the same native coordinator/worker setup without this store.
Keep the store only if it prevents lost/duplicated work or measurably reduces reconstruction.
Do not count extra records or agent activity as a benefit. One run is exploratory.

## Rooms, then cloud

Keep one authoritative controller outside the guests. Bind each guest's post-restore worker ID
to its attempt and use the existing transport/runtime; snapshots must not freeze a live identity.
First prove boot, command execution and artifact export. Then kill a worker during a job, preserve
its workspace, start a replacement and submit the old result late. Kill the controller between
recording a delivery intent and sending it; reconcile uncertain launches before retrying.

Fail on accepted stale results, duplicate launches after uncertain delivery, lost acknowledged
requests, missing result artifacts, or leftover workers after cleanup. External effects need
fencing at their destination; our local acceptance token does not supply that.

Only after local Rooms passes should this run in cloud with a fixed spend cap and teardown plan.
No new autoscaler is required for any of these tests. Start workers because there is useful
independent work or a neglected responsibility, not because the queue exceeds a magic number.
