# flare

The workbench's escalation/block **routing sink** — an Observability tool, not a
plane. A small Go binary that tails producers' artifact logs (gate `log.jsonl`,
ship `receipts.jsonl`) and, on block/escalate, delivers a Slack page — a
`chat.postMessage` with a severity-colored Block Kit card that leads on the
required action and carries a `View PR` button when the event names a repo and PR
number. Toast and webhook are the other available channel *types*.
Pure sink — it never gates, never blocks, never writes into a producer's state
or takes a producer's lock.

**Not to be confused with `contracts/escalation` / `cmd/escalate`.** flare is the
*outbound* arrow (system → human, read-only routing). It routes an escalation
*out*; it never ingests the human's decision *back* — that inbound arrow is
`cmd/escalate` → `gate resolve`, a separate component precisely because Amendment
3 forbids a read-only sink from writing a decision. "escalation-routing" is
flare's cargo, not a plane it owns; flare serves Observability.

`docs/DESIGN.md` is the contract: sources and their read shapes, the routes
table, dedupe/throttle, cursor integrity, and the non-goals. Change behavior
there first.

## Layout

- `cmd/flare` — verbs: `watch`, `sweep`, `status`. Owns the cycle policy
  (cursor advances only when every event from a source settled).
- `internal/source` — tail + parse producers' JSONL into events, decoding the
  shared verdict + envelope types from the `contracts` package (no hand-rolled
  parser). Mechanism only; knows nothing about routing.
- `internal/route` — the declarative routes table + severity-monotone
  throttle. All policy comes from config.
- `internal/notify` — one event to one channel. `slack` posts to
  `chat.postMessage` with a bearer token and renders a severity-colored Block
  Kit card (`renderSlackMessage` → `slackBlocks` → `actionElements`); delivery
  counts only on HTTP 200 **and** `"ok": true` in the body. The actions row
  carries the `View PR` link and — when the channel opts in with
  `resolve_actions` AND the event is a resolvable park (`resolvablePark`) — the
  **Approve/Block** interactive buttons (render-only; the tap is `escalate`'s,
  never flare's — Amendment 3). `toast` shells `powershell.exe` 5.1 (pwsh 7
  cannot project WinRT); `webhook` POSTs the event JSON via `net/http`.
- `internal/journal` — flare's private state under `~/.flare`: append-only
  delivery journal (the dedupe substrate) + cursors with the `last_poll`
  liveness fact.

## Invariants (pinned by tests — keep them pinned)

- An event matching no route goes to the catch-all channel; silence requires
  an explicit `drop` route. Absence of a route must not read as
  not-page-worthy.
- Dedupe keys on stable event IDs (gate artifact ID; receipt key+outcome);
  a restart or resweep never re-pages.
- The gate cursor pins the last processed chain hash; a mismatch or a
  shrunken file fires a cursor-alert notification and resweeps — never a
  silent reset.
- Throttle is severity-monotone: a strictly worse event passes an open
  window (worst wins).
- A corrupt artifact line fails the read loudly; it must not read as quiet.
- Errored deliveries stay unsettled (the cursor holds) so they retry;
  delivered/dropped/throttled settle.
- Single-instance: `watch`/`sweep` take an OS lock on `~/.flare/watch.lock` and
  refuse (exit 3) if another flare holds it — two writers corrupt the state.
  `status` never locks. Cursor saves use a unique temp (never a shared name).
- A corrupt `cursors.json` is recovered, not fatal: quarantined aside +
  `cursor-alert` + resweep from empty — it must never silently wedge the loop.
- Resolve buttons render ONLY for a resolvable park (kind `escalation` + an
  artifact id + the grant it ran under) on an opted-in channel; a
  verdict-escalate, a cursor-alert, a park missing its id, or a grantless park
  (including the documented `none:`-prefixed sentinel, which lifts as
  grantless) never gets Approve/Block — a grantless park resolves out-of-band.
  flare renders the button; it never handles the tap (the callback targets
  `escalate serve`).

## Checks

```
gofmt -l . && go vet ./... && golangci-lint run ./... && go test ./...
```

The sole in-repo dependency is the `contracts` package.
