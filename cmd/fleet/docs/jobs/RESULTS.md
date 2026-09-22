# First Fleet job service slice

A single Fleet binary now exposes durable jobs through CLI and loopback HTTP. Existing
watcher workers can pull jobs; queue operations do not call a model or launch sessions.

The [recorded process drill](local-receipt.json) passed with source hashes attached:

- Two independent worker processes claimed different jobs and returned independently checked hashes.
- API SIGKILL/restart preserved reported artifacts before coordinator acceptance.
- A killed claimant's lease expired; replacement received a new token; late completion refused.
- Replaying a claim request recovered one attempt. Rejection queued a fresh repair attempt.
- Metrics agreed with the observed run: two accepted jobs, five attempts, two retries, one repair running.

Go race tests cover simultaneous claims, lease renewal/expiry, conflicting retries, acceptance,
queue order, worker exclusivity, malformed/semantically invalid storage and clock rollback.
API tests cover shared state, JSON errors and rejection of browser/rebinding requests.
Scoped lint and vet passed. Full repository tests run in CI; the local broad invocation was
refused by the normal slow-command hook, and was not bypassed.

Independent code review found and fixed two defects: time-dependent corruption validation
under clock rollback, and plaintext HTTP errors breaking JSON clients. Renewal also now
preserves the longer existing expiry. A pre-existing vocabulary check caught a comment;
it was corrected without changing the check.

This is not yet an unattended LLM course-production run, an authenticated remote service,
a throughput benchmark, or a power-failure test. Workers in the drill are deterministic.
State is local Linux/macOS; tokens fence results only. The generic queue does not check
Ivy's teaching criteria or dependency graph. A reported artifact requires separate acceptance.

Next: configure an isolated Fleet watcher coordinator and workers to execute one real
consumer job, inject an interruption, and compare manual interventions and model usage to
the native Ivy baseline. No second scheduler or state store is needed for that run.
