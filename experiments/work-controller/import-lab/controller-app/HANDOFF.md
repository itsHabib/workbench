# Pressure checkpoint

Active controller job: pressure-import-desk, worker import-builder, token 2.

## Observed baseline failures

- The external pressure run passed 50,000-row throughput, concurrency, and restart persistence.
- Unterminated CSV quotes were accepted and could yield valid records.
- A request over 10 MiB ended as a disconnect/error instead of a clear 400/413 response.
- Reusing a request_id with a different CSV created a second import.
- Real Chromium could submit and show row errors, but the 390px viewport overflowed horizontally.

## Proposed choices

- Keep the stdlib single-process service and JSON persistence; add strict CSV error handling so malformed input produces no records.
- Enforce a 10 MiB request limit before decoding the JSON body and return a clear 413 response.
- Persist a request-id fingerprint alongside each import: same ID plus identical CSV returns the original import; same ID plus different CSV returns 409.
- Preserve the small synchronous path and make shared store access safe for concurrent requests; avoid introducing a queue or database without evidence it is needed.
- Adjust the existing compact layout and table styles for narrow Chromium viewports, while retaining file input and readable row results.

## Exact next checks

1. Focused unittest: malformed unterminated quote, body limit, request-id replay/conflict, restart persistence, and concurrent requests.
2. HTTP smoke: /health remains responsive during a large request; 10 MiB boundary and over-limit responses are clear; replay/conflict statuses and bodies match the seam.
3. Browser check with a real CSV filepath in Chromium at 390px: submit, inspect row-numbered errors, retry corrected input, and confirm no horizontal overflow.
4. Re-run the supplied pressure checks only after the focused checks pass; report any pending-work restart interval as not covered if none is observable.

No app code was changed in this checkpoint.

