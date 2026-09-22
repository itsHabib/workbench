# Durable jobs for an agent coordinator

`fleet job` holds work while agents decide what to do with it. It adds a local queue,
renewable claims, recorded attempts, returned results and explicit acceptance to Fleet.
The CLI and loopback HTTP API operate on the same store. No model is called by a queue operation.

A coordinator agent decides what to submit, whether to reuse a worker, and whether a
returned artifact meets the request. Code makes claims and transitions atomic. Fleet's
existing watcher remains the session launcher; this API is not another launcher.

## Try it

Build from the repository root: `go build -o /tmp/fleet ./cmd/fleet`.
Use a disposable directory for this example:

```sh
/tmp/fleet job submit --state /tmp/fleet-job-demo --id lesson-1 --brief 'Create an explanation and executable exercise; return artifact path and test evidence'
/tmp/fleet job claim --state /tmp/fleet-job-demo --worker author-1 --key lesson-1-attempt-1 --ttl 300
```

Save the returned job ID and attempt token. Renew before expiry while working:

```sh
/tmp/fleet job renew --state /tmp/fleet-job-demo --id lesson-1 --worker author-1 --token TOKEN --ttl 300
/tmp/fleet job complete --state /tmp/fleet-job-demo --id lesson-1 --worker author-1 --token TOKEN --result 'artifact path, revision, checks, and limitations'
/tmp/fleet job accept --state /tmp/fleet-job-demo --id lesson-1 --token TOKEN --evidence 'independent checks of this exact artifact'
/tmp/fleet job metrics --state /tmp/fleet-job-demo
```

`retry --id ... --token ... --evidence 'specific repair request'` returns a reported
job to the queue; an expired attempt can also be retried. Old tokens cannot complete
or renew a replacement attempt. Reusing a claim key recovers the original claim after
response loss; use a new key for a new attempt. Submission IDs are immutable retry keys:
submitting changed work under an existing ID refuses instead of overwriting it.

A result is not acceptance. Evidence text records a coordinator decision; it does not
run a verifier. Changes to the requested outcome need a new job ID, with the source
revision and dependencies in the brief. Domain-specific dependency validation stays
with the consumer (for example Ivy), not the generic queue.

## HTTP API

```sh
/tmp/fleet job serve --state /tmp/fleet-job-demo --listen 127.0.0.1:7381
curl -sS http://127.0.0.1:7381/v1/jobs -H 'Content-Type: application/json' \
  -d '{"op":"submit","id":"visual-1","brief":"Review the rendered diagram against source revision X"}'
```

POST `/v1/jobs` accepts the same operation names and fields as the CLI; `ttl_seconds`
is the JSON lease duration. Supply it for claim/renew. `list`, `get` and `metrics` are
read-only operations. Responses are JSON; invalid requests return 400, missing jobs 404,
conflicting transitions 409, and store failures 500. No CORS/browser clients are enabled.
Numeric loopback binding and Host checks prevent accidentally exposing this trusted-local
API. There is no tenant authentication: local processes can act as any worker/coordinator.
Do not expose this listener through a public tunnel.

## Connect jobs to actual agents

Add `jobs` to an existing `deliver.json` worker. The watcher claims before launch;
the provider bridge renews while working and reports the final result. Empty queues
start no model. The coordinator still decides acceptance.

```json
{
  "worker-1": {
    "cwd": "/absolute/path/to/worker",
    "provider": "claude",
    "model": "haiku",
    "max_budget_usd": 1,
    "prompt": "Implement the supplied job; commit changes and report checks and limitations.",
    "jobs": {"state": "/absolute/path/to/jobs", "id": "eligible-job", "ttl_seconds": 300}
  }
}
```

Use the existing address binding described in [headless.md](../headless.md). Omit `id`
to pull the oldest eligible job. Bind only work that can safely run in that worker's
checkout. A different job starts a fresh conversation; a same-job retry can resume
its observed terminal session. Prior results and retry feedback travel in the prompt.
Do not tell an automatically bound worker to claim or complete the job itself.

The bridge records the returned summary, Git revision and dirty status, actual provider
session, raw trace reference and reported usage. It does not commit files or run acceptance
checks for the coordinator. A failed report retains the artifact for reconciliation.
Lease renewal failure requests provider interruption and prevents reporting success.
Claims survive watcher replacement because renewal runs with the provider bridge.

`max_budget_usd` is optional, positive, and Claude-only; it passes the SDK's per-turn
limit. It is not a fleet-wide spending account. `fleet run-report --json` includes job IDs
for bound worker attempts; missing provider usage remains unknown.

For a complete local setup, use the [goal launcher](../../../../experiments/agent-work-loop/README.md).
It provisions one coordinator and two available workers using these same primitives.
The [coordinator card](coordinator.md) remains editable prose. Manual CLI/HTTP workers
may still use [worker.md](worker.md); automatic binding is optional.

## Failure semantics

- Mutations serialize through a kernel file lock, then replace a single durable snapshot.
  There is no lock-age heuristic. Killing the lock holder releases the lock.
- Leases use the store host clock. Expired attempts are eligible for a new claim; no reaper
  or model call is required. Clock jumps affect expiry. Renewal must precede expiry and never shortens the current expiry. Reusing a
  worker retires its expired attempts permanently, even if the clock later moves backward.
- Attempts fence queue results, not filesystem/network effects. A lease expiring does not
  kill a worker. Use isolated worktrees/Rooms and idempotency or effect-side fencing before
  retrying work with external effects. Worker names are caller-supplied, not authentication.
- One active lease per worker prevents concurrent claims under that identity. Use unique
  worker identities for separate live sessions, including restored clones.
- This slice targets one trusted Linux/macOS host and a local filesystem. Windows
  directory-sync durability is not qualified. Workers in separate Rooms must use an
  authenticated central service in a future extension, not copy/share these state files.
- Snapshot persistence is deliberately simple; each mutation rewrites retained history.
  This is not a high-throughput distributed queue. Measure a real bottleneck before adding
  a database backend, autoscaler or generic dependency scheduler.

Acceptance criteria for the first slice: concurrent workers cannot claim the same attempt;
response-loss retries recover one claim; lease expiry permits recovery; late completion
and renewal refuse; completed results survive process restart; unaccepted work remains
visible; corrupted storage never becomes an empty queue.

## Reproduce recovery

```sh
go test -race ./cmd/fleet/internal/jobs ./cmd/fleet -run 'TestJob|TestConcurrentClaims|TestExpiryAndReplay|TestReviewTransitions|TestQueueWorkerLimitAndMetrics|TestReadOnlyAndCorruption|TestSubmitRenewAndRejectedInputs'
python3 cmd/fleet/e2e/jobs.py /tmp/fleet /tmp/fleet-jobs-receipt.json
```

The black-box drill runs two deterministic OS worker processes through HTTP, kills and
restarts the service after result reporting, independently verifies those artifacts, kills
a claimant before completion, waits for expiry, and rejects its late result. It also checks
claim-response replay and review-driven repair. It makes no LLM coordination or teaching
quality claim. CI runs this drill in addition to the Go race tests.
