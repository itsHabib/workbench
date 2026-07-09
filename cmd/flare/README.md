# flare

One stdlib-only Go binary that pushes a notification when something in the
workbench blocks or escalates. It tails the artifact logs other tools already
emit — gate's state log, ship's run receipts — matches events against a small
routes table, and raises a Windows toast or posts a webhook. A parked run
should not have to wait for someone to ask.

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
```

Config lives at `~/.flare/routes.json` (`-config` overrides); flare's own
state (delivery journal, cursors) at `~/.flare/` (`-state` overrides). See
`docs/DESIGN.md` for the config shape.

## Develop

```
go build -o flare.exe ./cmd/flare
gofmt -l . && go vet ./...
golangci-lint run ./...
go test ./...
```

Constraints that are design decisions, not omissions:

- **Standard library only.** The toast shells to `powershell.exe`; the
  webhook is a `net/http` POST. Nothing else.
- **Reads are raw and read-only.** No producer lock is taken, torn final
  lines wait for the next poll, watched paths are explicit config.
- **Nothing is silently dropped.** Unrouted events hit a required catch-all
  channel; throttled and dropped events are journaled; a broken cursor chain
  fires a notification itself.
- **Nothing leaves the box unless configured.** There is no default webhook
  URL.
