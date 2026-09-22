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

## Use existing Fleet workers

Give an existing `fleet watch` worker the [worker card](worker.md) and a coordinator the
[coordinator card](coordinator.md). Configure their normal Fleet addresses/recurring wakes
as in [headless.md](../headless.md). Workers pull claims on their next wake. Submitting a
job does not itself send Fleet mail or spawn a session; the coordinator can use existing
`fleet send` for a prompt wake. A crash between submit and send leaves durable queued work
for the next scheduled poll. Do not maintain a second assignment in `fleet request` for
the same job. Fleet runtime occupancy and job claims are different records, not competing
sources of job ownership.

Start with one coordinator and two workers. Prefer cheaper workers for bounded tasks;
use the coordinator's judgment to escalate difficult work. Use `fleet run-report` for
provider usage and job `metrics` for queue state. Usage is not yet attributed to job IDs.

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
