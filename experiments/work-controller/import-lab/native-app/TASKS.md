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
