# Import desk external-check protocol

Status: v1 frozen before either candidate implementation or its tests were
inspected. The narrow v2 correction below was made after the first baseline run.

## V2 oracle correction

The first baseline exposed two requirements that v1 had added beyond the shared
brief. V2 removes them without changing any candidate:

- A browser UI needs a CSV input surface and submit control, but does not need a
  literal HTML `form`; JavaScript-driven controls satisfy the stated seam.
- A mixed-validity import may use either terminal status. The shared status
  vocabulary includes `failed`, and the functional contract requires returned
  valid records plus row errors rather than prescribing `completed` for a
  partial result.

The original v1 results remain retained as false-positive evidence. No pressure
or recovery threshold changed.

This protocol evaluates each app only through the launch and HTTP seams in the
shared brief. It is a conformance report, not a self-reported score and not a
winner selection. Every check records one of:

- `pass`: the externally observed behavior met the stated threshold.
- `fail`: the app responded, but the behavior violated the contract.
- `not_covered`: the check could not exercise the intended condition. This is
  not success. The recovery round uses this when work completes before kill.
- `error`: the harness could not obtain trustworthy evidence, including launch,
  transport, timeout, or malformed-response failures.

The harness retains a JSON result, a chronological JSONL exchange log, server
stdout/stderr, and the round's data directory. It never imports app code or
uses app tests. Each invocation gets an unused loopback port, launches
`python3 server.py --port PORT --data-dir DIR`, and signals only that owned
process. The per-round wall-clock budget is about 30 seconds; startup gets 5
seconds and individual ordinary HTTP operations get 5 seconds unless a check
below sets a tighter threshold.

## Common interpretation

`POST /imports` must return JSON with a nonempty `id` and a status in
`queued`, `running`, `completed`, or `failed`. For `queued` or `running`, the
harness polls `GET /imports/ID` until `completed` or `failed`. A terminal import
must expose arrays named `records` and `errors`. HTTP 4xx responses used for
contract rejection may contain any JSON object with a useful nonempty error or
message string.

Record comparisons are semantic: field values must match exactly, while list
order is not material unless duplicate IDs make order the only discriminator.
An invalid input row must not occur in `records`; each expected error must carry
an integer row number and a nonempty message. CSV row numbers are physical,
one-based lines including the header.

## Round `build`

1. **Startup and health.** The server becomes reachable within 5 seconds.
   `GET /health` returns HTTP 200 and a JSON object.
2. **Browser surface.** `GET /` returns HTTP 200 HTML containing a CSV input
   surface (file input or textarea) and a submit control. The HTML must
   not depend on an external `http://` or `https://` script, stylesheet, image,
   or module URL.
3. **Mixed CSV validation.** Submit one CSV containing two valid rows and three
   invalid rows: empty `id`, missing the right side of `@`, and an address with
   a space. The terminal result (`completed` or `failed`) must contain exactly
   the two valid records, no invalid record, and useful errors at physical rows
   3, 4, and 5.
4. **Corrected retry.** Submit corrected content as a new request. It must
   complete with all corrected records and no row errors.
5. **Inspection/listing.** `GET /imports/ID` must reproduce the terminal result,
   and `GET /imports` must include summaries for both import IDs with their
   terminal statuses.

## Round `pressure`

The later requirements are treated as contract requirements, not hidden bonus
tests, and must be supplied to both builders at the same readiness point.

1. **Large valid import.** Submit exactly 50,000 data rows whose JSON request
   body is below 10 MiB. It must complete within the round budget with exactly
   50,000 records, no errors, and exact first/last sentinel records.
2. **Ten-MiB cap.** A JSON request body larger than 10 MiB must be rejected with
   HTTP 400 or 413 without creating an inspectable successful import.
3. **Malformed CSV.** An unterminated quoted field must be rejected with HTTP
   400/422 or produce a terminal `failed` import with a useful error. It must
   not yield records from the malformed input.
4. **Concurrent service.** While a large import request is in flight, issue
   `GET /health` and a small valid import from separate clients. Health must
   return HTTP 200 JSON within 2 seconds; the small import must be accepted and
   complete correctly within 5 seconds. If the large request finishes before
   the probes begin, repeat with concurrent large requests; a failure to create
   overlap after that is `not_covered`, not a pass.
5. **Restart persistence.** After a completed import, terminate the owned server,
   restart it on a new unused loopback port with the same data directory, and
   inspect the same ID. Status, records, and errors must match the pre-restart
   terminal result.
6. **Request-ID idempotency.** Twice submit identical CSV with the same explicit
   request ID. Both responses must identify the same import, and listing/data
   must show no duplicate records or second job. Submit changed CSV with that
   request ID; it must return HTTP 409 and leave the original import unchanged.

## Round `recovery`

Use a near-limit valid request to maximize the chance of observing accepted,
unfinished work. Submit it with an explicit request ID. Once the POST has
returned an ID in `queued` or `running`, immediately send SIGKILL to the exact
owned server process. Restart with the same data directory and a new unused
port, then follow that ID for up to 20 seconds.

The check passes only if the accepted import reaches `completed`, contains each
expected record exactly once, has no row errors, and listing contains exactly
one summary for that ID. A terminal `failed` import, missing ID, lost work,
duplicate job, or duplicate record is a failure. If POST does not return before
completion, or returns `completed` before kill, the intended interruption was
not observed and the result is `not_covered`. The harness must never translate
that case into recovery success.

Native worker/session interruption is outside this process-level protocol and
is recorded separately by the experiment coordinator.
