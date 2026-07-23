# flare — the escalation-routing plane (v0)

**Status:** v0 design, 2026-07-08
**Owner:** operator
**Shape:** one small Go binary, outside ship and gate. Pure sink.

## Problem

Every pipeline in the workbench escalates by parking: gate parks for judgment and exits 2
(park-only by design), the driver parks runs at `awaiting_judgment`, ci-classify's spec routes
`infra → page` at a slot gate's verdict ladder deliberately does not have. Nothing pushes any
of that outward — a parked run waits for the operator to ask (workbench-redesign recon #6,
RED-TEAM #9). The gate's own backtest showed parking is the *hot* path (5/7 real PRs), so
silence-until-polled is the standing failure mode.

flare closes the seam: it watches the artifact logs those planes already emit and pushes a
notification — a Slack page (a `chat.postMessage` Block Kit card), with Windows toast
and webhook as the other available channel types — when something blocks or escalates. It
is the push half; `/wip` and `/status` remain the pull half.

**Posture, stated plainly: flare is best-effort push over an authoritative pull. The artifact
logs remain the source of truth; flare only shrinks time-to-notice.**

## Laws (from the workbench-redesign steward consult, 2026-07-08)

- **Pure sink.** flare reads emitted artifacts and pushes notifications. It never gates, never
  blocks, never writes into any producer's state. It is Observability-shaped (redesign
  Amendment 3: push-on-block is an Observability view).
- **Artifacts are the channel — consumed in place.** Tailing a producer's append-only log is
  the sanctioned read; a neutral drop-dir of copies was considered and **rejected** (it
  recreates the one-fact-two-records disease the redesign opens with). If a future producer
  has no artifact log of its own, that is the trigger to revisit.
- **Watched paths are config, never derived.** No hardcoded sibling paths (the tracelens
  path-mirroring decay mode; also the MSIX trap — `%APPDATA%/ship` resolves to two different
  stores for connector- vs terminal-launched processes, so the routes file names one absolute
  path explicitly).
- **No producer internals.** flare decodes the JSONL envelope and verdict via the shared
  `contracts` package (its published types + schema) — never gate's `internal/verify`, and it
  never takes gate's writer lock. Sharing the *contract* is wanted; importing the producer's
  *decision code* is the one forbidden import. Reads tolerate a torn final line (process up to
  the last complete newline).

## Sources (what lands on disk today)

| source kind | path (example; set in config) | what flare lifts |
|---|---|---|
| `gate-log` | `pers/gate/state/log.jsonl` | envelope `{id, kind, run, time, parents, body, prev, hash}`; events: `kind=escalation` (body `{question, code?, outcome?}` — the notification body, ready-made) and `kind=verdict` with `decision ∈ {block, escalate}` |
| `ship-receipts` | `%APPDATA%/ship/receipts.jsonl` (Roaming, absolute) | receipts with `outcome ∈ {failed, cancelled}`; key = `key` + `outcome` |

ci-classify needs no third source: its verdicts record into gate state (gate PR #10), so the
gate tail carries the `infra` escalations when that rung ships.

**Read contract:** envelope first (identity, ordering, dedupe), per-kind body extractors
second. The envelope and verdict types come from the `contracts` package (schema
`verdict-v0.3.0.json`, v0.3.0), read as a **tolerant reader** — unknown fields ignored,
nothing required beyond what routing uses. Non-verdict bodies (escalations, receipts) are
their own shapes; `decision`/`tier` are never required of them.

## Pipeline: watch → match → route → dedupe/throttle

1. **Watch.** Poll each source file on an interval (default 60s). Cursor per source persists
   across restarts. Catch-up is mandatory: on start, sweep from the cursor and route
   everything missed — a late toast beats never.
2. **Match.** The source parser decides what is an *event* at all (gate: every escalation,
   every non-pass verdict; ship: failed/cancelled receipts). Every event is push-worthy by
   construction.
3. **Route.** A declarative routes table (see config) picks the channel. An event matching no
   route goes to the **catch-all channel** — absence of a route must not read as "not
   page-worthy". Silence requires an explicit route to the `drop` channel.
4. **Dedupe/throttle.** Dedupe on artifact ID (gate) / `key+outcome` (ship): a
   restart-and-resweep never re-pages. Throttle is per-route (min seconds between pushes for
   the same route) and **severity-monotone**: a strictly worse event (block > escalate >
   failed > cancelled) passes through an open throttle window — worst wins, the reducer's
   monotone spirit applied to notifications.

## Cursor integrity (absence must not read as calm)

- **gate-log:** the cursor stores byte offset + the hash of the last processed line. On poll,
  the first new line's `prev` must equal the stored hash; the file shrinking below the offset
  means truncation. Either mismatch **fires a flare itself** (`cursor-alert`) and resweeps
  from zero (dedupe prevents re-paging) — never a silent reset.
- **ship-receipts:** offset only (no chain); shrink → same alert + resweep.
- **watcher-down:** flare cannot supervise itself in v0. Honest mitigations: `watch` updates a
  `last_poll` timestamp every cycle; `flare status` exits non-zero when that is stale (wired
  into the sign-on `/health` surface, where the operator already looks); catch-up on start
  covers the gap after the fact.

## Channels

- `slack` — the delivered surface. `net/http` POST to Slack's `chat.postMessage` using a
  configured bot token and channel ID. The event renders as one severity-colored Block Kit card
  (`renderSlackMessage`): a header that leads on the required action, a blockquoted *why*, a
  primary `View PR` button when the event names a repo+number, and a small-print context footer;
  the attachment's fallback is the lock-screen line. Delivery requires HTTP 200 **and** an
  `{"ok":true}` response because Slack reports API errors in HTTP 200 bodies. The bot needs
  `chat:write` and membership in the target channel. The token lives only in the operator's
  local routes file and is never written to errors or logs.
- `toast` — Windows toast via `powershell.exe` 5.1 WinRT (`ToastNotificationManager`).
  Verified on this box 2026-07-08; pwsh 7 cannot project WinRT types, so the shell-out targets
  `powershell.exe` explicitly. Zero config.
- `webhook` — `net/http` POST of the event JSON to a configured URL. **No default URL; nothing
  leaves the box unless the operator configures it.**
- `drop` — explicit silence (the only way to silence a matched event).

Delivery is at-least-once-attempted, best-effort. A channel failure is journaled and the event
stays undeduped, so the next cycle retries.

## The journal (`~/.flare/`)

flare is not a State writer (writing into gate's anchored log would fail its audit) and not
storeless (dedupe/cursor/delivery facts must live somewhere). It keeps a private delivery
journal no other plane reads:

- `~/.flare/journal.jsonl` — append-only `{time, kind: delivered|skipped-dedupe|
  skipped-throttle|cursor-alert|error, source, event_id, channel, note}`. Answers "was the
  operator paged at T". flare explains delivery; producers explain decisions.
- `~/.flare/cursors.json` — per-source cursor + `last_poll` (the liveness fact `status` reads).
- Config default: `~/.flare/routes.json` (`-config` overrides).

A full `notified` artifact written back into State is parked; trigger: `explain` demonstrably
needs delivery facts to reconstruct a decision.

## Config (`routes.json`, versioned from day one)

```json
{
  "version": 1,
  "poll_seconds": 60,
  "sources": [
    {"name": "gate", "kind": "gate-log", "path": "C:/Users/<you>/pers/gate/state/log.jsonl"},
    {"name": "ship", "kind": "ship-receipts", "path": "C:/Users/<you>/AppData/Roaming/ship/receipts.jsonl"}
  ],
  "channels": {
    "toast": {"type": "toast"},
    "phone": {"type": "slack", "token": "<bot-token>", "channel": "<channel-id>"}
  },
  "routes": [
    {"match": {"source": "gate", "kind": "escalation"}, "channel": "phone"},
    {"match": {"source": "gate", "kind": "verdict", "decision": "block|escalate"}, "channel": "toast", "throttle_seconds": 600},
    {"match": {"source": "ship", "outcome": "failed|cancelled"}, "channel": "toast", "throttle_seconds": 300}
  ],
  "catch_all": "toast"
}
```

An unknown `version` major is refused. Match fields are exact strings with `|` alternation;
omitted = any. When a match needs logic the table can't express, that is a signal to fix the
*producer's* artifact (structured field), not to grow a rules engine here.

## CLI

- `flare watch` — poll loop (catch-up sweep first, then tick).
- `flare sweep` — one catch-up pass, then exit. Exit 0 = swept clean; non-zero = config/source
  error.
- `flare status` — JSON health (last poll, per-source cursor, journal tail). Exit 0 healthy,
  1 stale/never-ran, 2 config error.

## Non-goals (v0)

- No gating, no blocking, no write-back into any pipeline (so no adversarial gate pass owed —
  if that ever changes, the pass is owed first).
- No acking/resolution workflow — flare notifies; the operator acts in the producer's surface.
- No cloud infra, no daemon manager, no self-supervision beyond `status` + catch-up.
- No rules engine; the routes table is the whole policy surface.
- No reading ship's SQLite, ever (a sink in another engine's live database is the side-channel
  Amendment 3 forbids).

## Integration edges handed to owners (not built here)

1. **ship owner:** driver `awaiting_judgment` parks live only in SQLite — not readable by a
   sink. Ask: emit a park receipt to `receipts.jsonl` (or an artifact log) at the
   `awaiting_judgment` transition. Until then flare covers failed/cancelled receipts only —
   this is the push-on-block gap that remains, and it is an emission gap, not a flare gap.
2. **gate agent:** (a) publish the artifact *envelope* schema next to `verdict-schema-v0.json`
   so external readers stop parsing against prose; (b) when ci-classify's `infra` findings
   land, carry page-worthiness in a structured field (`Finding.severity` exists) rather than
   the `infra: <sig> — wants page` title prefix — flare will match on the title prefix as a
   v0 stopgap and wants to delete that matcher.
