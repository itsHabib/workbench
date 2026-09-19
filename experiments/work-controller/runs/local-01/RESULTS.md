# Local controller experiment

**Built and exercised:** coordinator/worker cards, a durable local job lifecycle, fenced result
acceptance and an outbox. No automatic model routing service, process launcher or Rooms deployment.

## Observed

- Ten independent-parent test executions passed: concurrent claims, one running job per worker,
  replay/conflict handling, rollback on invalid routing, persisted state and stale-result rejection.
- The 26-call CLI drill passed. Each invocation was a fresh process reading persisted state.
  It exercised duplicate input, queued follow-up, spawn intent, reassignment and late completion.
- An actual native subagent built the lifecycle. While it held a running claim, the parent
  coordinator assigned a related follow-up to that same worker. The second claim refused while
  busy; after reporting the first result, the worker claimed and completed the follow-up.
- The parent inspected the implementation and independently ran tests before accepting both results.
  Source hashes are in accepted.json. This acceptance covers the experimental implementation,
  not production safety, merge authority or effectiveness versus a simpler native workflow.

## What this does not demonstrate

The worker was launched before instrumentation began. The coordinator was this interactive agent;
its routing decision was native judgment, not a model called by the program. No additional worker
was launched by a queue event. The spawn path was tested as a durable intent only. We did not kill
a live worker or restart this coordinator session; fresh CLI processes prove store persistence,
not successful reconstruction of judgment. There was no cross-branch integration conflict.

The follow-up found a useful operational detail: a lost claim response is recoverable from a
snapshot, but only under a single-consumer worker identity. Pending launch intents can become
stale; checking a snapshot cannot atomically protect an external launch. These are next adapter
problems, not reasons to claim exactly-once execution now.

## Next

Run the cards on a real multi-part feature using existing Fleet/native launch and mail. Compare
against the same team using native tools alone. Add transport integration only at the first
observed loss/reconstruction problem. Then run the Rooms failure drill described in RUNS.md.
No cloud resources or paid model endpoints were invoked by the prototype.

## Receipts

- drill.json: deterministic CLI trace, including expected failures.
- live-start.json / followup.json: coordinator assignment decisions and busy snapshot.
- worker-receipt.json: real worker claims, reports and recovery investigation.
- accepted.json: parent acceptance evidence, final registry and implementation hashes.

Tests also run in the path-scoped Work controller experiment GitHub workflow. Full Go checks
are unrelated to this Python-only implementation; the local hook refused a full go test ./...
run and directed it to normal repository CI. No guard override was used.
