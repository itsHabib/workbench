# Import desk round 1

## Architecture choice

- `server.py` is a small Python standard-library app with a locked, in-memory
  import store. Imports are processed synchronously because this round is for
  one local user and small files, while the HTTP response still returns an ID
  that clients can follow.
- CSV parsing uses `csv.DictReader`; every data row becomes either a validated
  record or a row-numbered error, and malformed headers/CSV are reported as
  errors rather than being dropped.
- The UI is inline HTML/CSS/JS served from `/`, with fetch-based submit,
  refresh, and retry controls. There are no packages or external assets.
- `--data-dir` is accepted as part of the launch seam but is intentionally not
  used for persistence in this one-user round.

## Work

- [x] Implement health, browser UI, import submission, detail, and list routes.
- [x] Add validation and endpoint unit tests.
- [x] Run the requested test suite and an HTTP/browser smoke check.

## Round 2 pressure checkpoint (plan only)

Observed in the supplied `local-01` pressure run:

- 50,000 rows and concurrent imports passed; malformed CSV passed.
- Restart persistence failed (`404` after restart).
- Bodies above 10 MiB were accepted (`201`) instead of rejected.
- Reusing a `request_id` created a different job, including for the same CSV.
- Real Chromium reported `Unexpected token const`; clicking the UI did not POST.
- Browser flow still needs a file-path upload and readable row-level results.

Proposed bounded choices:

- Use one SQLite file under `--data-dir` (stdlib `sqlite3`) for completed import
  records, errors, original CSV, and request-id fingerprints. A schema-backed
  store gives restart durability and an atomic request-id lookup without adding
  a queue or service dependency.
- Set a 10 MiB request limit before decoding JSON; return a clear `413` with a
  JSON error. Keep parsing synchronous and use the existing threaded server so
  health and small concurrent imports remain responsive.
- Make `request_id` idempotent for the same CSV and return `409` when reused
  with different CSV. Preserve the existing import ID response seam.
- Replace the fragile inline browser script with a browser-verified script and
  add a file input that reads a selected CSV into the submission flow. Keep a
  textarea fallback for quick edits and show result rows/errors in the page.

Exact next checks after implementation:

1. Run the focused unittest suite, including 10 MiB rejection, malformed CSV,
   idempotent/conflicting request IDs, restart persistence, and 50k-row parsing.
2. Run a live HTTP check for health responsiveness during a small concurrent
   import and verify the 5-second completion target.
3. Launch, import, stop, relaunch with the same `--data-dir`, and fetch the
   import detail to prove persistence.
4. Exercise the file picker and submit path in real Chromium, confirming no
   console parse error, POST activity, and readable valid/error row results.
