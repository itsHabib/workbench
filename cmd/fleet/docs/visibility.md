# Inspect agent activity

`fleet status --all --json` is the current board. `fleet inspect <address>`
returns one unambiguous current agent, latest branch and role handoff, up to 30
received messages (long fields excerpted), work records and visible trace tail.
Mailbox observation scans at most 512 directory entries and 2 MiB of records,
with a 64 KiB limit per record. A partial scan reports counts as unknown and
does not claim its messages are the newest in the mailbox.
Missing traces/mail appear as explicit errors alongside available evidence.
Neither command starts a watcher, migrates state or acknowledges messages.

`fleet trace <address>` exports a JSON envelope with `data` (native JSONL),
`source`, `partial`, `coverage`, `modified_at`, `file_bytes`, `fingerprint` and `lines`.
It reads the last 1 MiB from an observed regular file, drops a partial leading
record and excludes a torn final record. `lines` contains at most 100 visible
events. Identity comes from the current board, not a user-supplied file path.
A provider trace takes priority over a harness transcript, then launch output.
The fingerprint identifies the exported bytes; Console invalidates an earlier
diagnostic when the observed trace changes, including within the same session.

Console renders these projections at `/fleet`; see `cmd/console/README.md` for
startup. Keep Fleet, Console and TraceLens binaries from the same checkout.
Provider session fields appear when the corresponding runtime reports them.

The evidence has different meanings: process state is liveness, provider/hook
records are activity, handoffs are authored understanding, and work receipts
are completion evidence at a particular head. Unknown evidence stays unknown.
The timestamp view contains latest observations, not a complete event journal.
