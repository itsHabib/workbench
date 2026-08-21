# flare

One Go binary that pushes a notification when something in the
workbench blocks or escalates. It tails the artifact logs other tools already
emit — gate's state log, ship's run receipts — matches events against a small
routes table, and sends a Slack page (a `chat.postMessage` Block Kit card that
leads on the required action, with a `View PR` button); Windows toast and
webhook are the other available channel types. A parked run should not have to
wait for someone to ask.

flare is a pure sink: it never gates, never blocks, and never writes into any
producer's state. **It is best-effort push over an authoritative pull — the
artifact logs remain the source of truth; flare only shrinks time-to-notice.**

Read `docs/DESIGN.md` first — it defines the sources, the read contract, the
routing/throttle rules, and the cursor-integrity behavior.

## Use

```
flare sweep     # one catch-up pass: route everything new, then exit
flare watch     # poll loop (catch-up first); default every 60s
flare status    # health as JSON; exit 1 when the watcher looks dead

flare sweep -from-start   # opt in to paging a source's WHOLE history (first run only)
```

Config lives at `~/.flare/routes.json` (`-config` overrides); flare's own
state (delivery journal, cursors) at `~/.flare/` (`-state` overrides). See
`docs/DESIGN.md` for the config shape.

**First run starts at the tail.** A source flare has never looked at (no
`cursors.json` yet, or a source newly added to the config) is placed at the
current end of its log and that placement is journaled as one `cursor-init`
entry — nothing before it is delivered. On a new machine the gate ledger is
history you already lived through, not a page queue; replaying it pages every
dead escalation into Slack until the API rate-limits. If you really want the
backlog, run `flare sweep -from-start` once before starting the watcher. A
*deliberate* reset (corrupt-cursor recovery, chain-break resweep) still resweeps
from offset 0 as before; dedupe keeps that from re-paging.

Slack delivery uses a bot token and channel ID, posts through
`chat.postMessage`, and treats the delivery as successful only when Slack
returns HTTP 200 with `{"ok":true}`. Keep the real credential only in the
local routes file; a channel definition looks like
`{"type":"slack","token":"<bot-token>","channel":"<channel-id>"}`. The bot
needs `chat:write` and membership in the target channel.

## Develop

```
go build -o flare.exe ./cmd/flare
gofmt -l . && go vet ./...
golangci-lint run ./...
go test ./...
```

Constraints that are design decisions, not omissions:

- **Reads are raw and read-only.** No producer lock is taken, torn final
  lines wait for the next poll, watched paths are explicit config.
- **Nothing is silently dropped.** Unrouted events hit a required catch-all
  channel; throttled and dropped events are journaled; a broken cursor chain
  fires a notification itself.
- **Nothing leaves the box unless configured.** There is no default webhook
  URL.
