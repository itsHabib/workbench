# Document import workload

Build the smallest useful local document import service in `fixture/`. It must
run with only Python's standard library:

```sh
python fixture/server.py --port 8765 --state /tmp/import-state
```

Serve a useful browser landing page at `/`; the API is the acceptance surface
and does not require a particular HTML form element. It accepts `POST /imports` with JSON
`{"request_id":"r1","csv":"title,body\\n..."}` and returns a stable
import identifier. `GET /imports/ID` returns the normalized imported documents.
Malformed JSON, missing fields, a wrong CSV header, or malformed CSV are 400.
Records survive a process restart in the supplied state directory.

Every request has an idempotency key. Repeating the same `request_id` with the
same payload returns the original result without a second import. Reusing it
with different bytes returns 409. Do not silently overwrite durable state.

Timed requests will arrive during a live run. Treat each as a real change in
scope and preserve earlier behavior:

- At +45s, add JSON document input: `{"request_id":"r2","documents":[{"title":"A","body":"B"}]}`.
- At +90s, add `GET /imports/ID/export`, emitting CSV with the exact
  `title,body` header and values that round-trip through CSV parsing.
- At +150s, test a persistence fault and retry requirement: a failed write must
  not leave a false successful import, and retrying the same request after a
  transient failure must either commit once or return a clear error.

Acceptance is an external black-box check. It uses more than 200 deterministic
synthetic CSV rows, exact data equality, malformed inputs, concurrent retries,
restart persistence, and a locked SQLite database write. The synthetic names
and text in `sample.csv` are public test
data; no credentials, network services, or third-party dependencies are
needed. Keep the implementation small enough for a cheap model team to finish
in roughly 5–10 minutes.
