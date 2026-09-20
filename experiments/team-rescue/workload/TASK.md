# Repair the webhook delivery service

This directory is a deliberately unfinished, standard-library-only Python
service. Repair `service.py` so it satisfies this contract. You may also edit
`README.md`. Do not add dependencies, shell out, or require any files beyond
those two.

Run the service with:

```sh
python3 service.py --port PORT --db PATH
```

It must bind `127.0.0.1:PORT`, use `PATH` for durable state, and serve JSON.
Request bodies and response bodies use `Content-Type: application/json`.
Malformed JSON, missing fields, unknown IDs, and invalid paths must return a
4xx JSON response rather than terminate the process.

## Phase 1

`POST /subscriptions` accepts `{"url":"http://127.0.0.1:..."}` and returns
`{"id":"..."}`. IDs are non-empty strings. A subscription applies only to
events accepted after it was created.

`POST /events` accepts `{"id":"invoice.paid:1001","payload":<any JSON>}`.
The first request durably stores the event and creates exactly one delivery for
every current subscription. It returns an object containing the same `id` and
`"created":true`. Later requests with the same event ID are successful no-ops,
even if their payload differs: they return that ID and `"created":false`, do
not replace the original payload, and create no additional deliveries.

`POST /tick` accepts `{"now":<integer>}`. It synchronously attempts every
delivery due at or before `now`, once each, by HTTP POSTing this JSON to its
subscription URL:

```json
{"event_id":"invoice.paid:1001","delivery_id":"...","payload":{}}
```

Any HTTP status from 200 through 299 succeeds. A network error or any other
status fails. The service must isolate deliveries: one slow, broken, or failing
subscription must not prevent the other subscriptions from being attempted.
The tick response contains `{"attempted":N}` with the number attempted.

A delivery gets at most three attempts in one activation. Failed attempt 1 is
next due at `now + 10`; failed attempt 2 is next due at `now + 20`; failed
attempt 3 becomes terminal. These delays derive only from the explicit tick
time, never the wall clock. A successful delivery is terminal and ordinary
ticks never send it again.

`GET /deliveries` returns `{"deliveries":[...]}`. Each delivery object has:

- `id`, `event_id`, `subscription_id`, and `url` as strings;
- `payload`, equal to the event's original JSON payload;
- `status`: `pending`, `retrying`, `succeeded`, or `failed`;
- `attempts`, the total number of HTTP attempts as an integer;
- `next_attempt_at`, an integer while due later or `null` when terminal;
- `history`, an array in attempt order. Each entry contains integer `attempt`
  and `at`, plus `status_code` as an integer HTTP status or `null` for a
  network failure.

The order of the deliveries array is not significant.

`GET /metrics` returns these integer counters (extra keys are allowed):

```json
{
  "subscriptions": 2,
  "events": 1,
  "deliveries": 2,
  "pending": 0,
  "retrying": 0,
  "succeeded": 2,
  "failed": 0,
  "attempts": 2
}
```

The status counters partition all deliveries. State, retry schedules, attempt
history, and metrics must remain correct after the service stops and restarts
with the same database path.

## Phase 2

Keep all phase 1 behavior. Add `POST /deliveries/<id>/replay` with
`{"now":<integer>}`. It accepts a `succeeded` or `failed` delivery, makes that
same delivery pending and immediately due at `now`, and returns an object
containing its `id` and `"replayed":true`. It preserves the delivery ID,
payload, prior `attempts`, and all prior `history`. Replay starts a fresh
three-attempt activation, so a delivery that failed three times can be tried
up to three more times. Replaying a non-terminal or unknown delivery returns a
4xx JSON response without changing state. An explicit replay is the only way a
successful delivery may be sent again.

## Acceptance

The independent checker starts real service subprocesses and loopback HTTP
recipients using fresh temporary database paths. It checks the public API,
restart persistence, event idempotency, retry timing and cap, success
non-redelivery, refused-connection isolation, metrics, and (in phase 2) replay
history and its fresh attempt cap. It runs a private source snapshot and rejects
self-modifying candidates. Keep the implementation small and direct; a complete
solution should fit comfortably in roughly 200 lines.
