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

- `cmd/flare` — verbs: `watch`, `sweep`, `status`, `digest`. Owns the cycle
  policy (cursor advances only when every event from a source settled) and the
  authority digest.
- `internal/source` — tail + parse producers' JSONL into events, decoding the
  shared verdict + envelope types from the `contracts` package (no hand-rolled
  parser). Mechanism only; knows nothing about routing. `ledger.go` is the
  read-only join a park needs — its grant and its verdict live in other
  artifacts, named by id — built lazily over bytes the read already holds.
- `internal/preflight` — policy leaf: given a park's grant ceilings, its
  verdict tier and its consumed review cycles, could an Approve tap actually
  land? Mirrors gate's ceilings (expiry → tier → cycles) rather than importing
  them (boundary law), and resolves every uncertainty to *unknown* so a button
  is withheld only on a proof. It decides what to PAINT, never what may merge.
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
  delivery journal + cursors. One `Load` replays the journal ONCE into
  everything a cycle needs (settled events, live cards, reason counts); cursors
  carry the `last_poll` liveness fact and any stalled source.

## Invariants (pinned by tests — keep them pinned)

- An event matching no route goes to the catch-all channel; silence requires
  an explicit `drop` route. Absence of a route must not read as
  not-page-worthy.
- One run announces itself ONCE. A gate run emits the reducer's fold and every
  component verdict it folded, and the fold restates the worst component's
  `why` — so routing on `decision` alone pages the same sentence once per rung.
  Routes select the fold with `dimension: "reducer"` and drop the rest. A
  parked run is announced by its ESCALATION artifact, never by its escalate
  verdicts, because only the escalation card is tracked for correction — a
  verdict card can never be closed, so a redundant one misreports live state
  forever.
- Dropping escalate verdicts cannot hide a park, and the reason is gate's
  inbox, not a census. gate appends one artifact per call, each with its own
  open/write/fsync (`cmd/gate/internal/state/state.go`), so the fold and the
  escalation are two writes with a window between them — a crash there leaves
  the fold on disk and no escalation. That run is not a park flare stayed quiet
  about: with no escalation artifact there is nothing in gate's inbox either,
  `gate next` shows nothing, and the caller saw the run die rather than park.
  flare mirrors gate's inbox; a run that never parked has nothing to announce,
  and the next gate run writes a fresh escalation. (The steady-state fact —
  385 reducer-escalate runs in the log, zero lacking the escalation — shows the
  ordering is not merely conventional, but it is the inbox that makes the
  silence correct.)
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
- A source with NO cursor yet (fresh state, or newly added to config) is
  placed at the current tail and that placement is journaled (`cursor-init`);
  the backlog is never delivered by default — `-from-start` is the one opt-in.
  Absent ≠ reset: a deliberate resweep (chain break, corrupt cursors) writes an
  explicit offset-0 cursor and still re-lifts the history (dedupe holds).
- A corrupt `cursors.json` is recovered, not fatal: quarantined aside +
  `cursor-alert` + resweep from empty — it must never silently wedge the loop.
- Resolve buttons render ONLY for a resolvable park (kind `escalation` + an
  artifact id + the grant it ran under) on an opted-in channel; a
  verdict-escalate, a cursor-alert, a park missing its id, or a grantless park
  (including the documented `none:`-prefixed sentinel, which lifts as
  grantless) never gets Approve/Block — a grantless park resolves out-of-band.
  A CEILING park (`grant_tier_exceeded` / `grant_cycle_exceeded`) is excluded
  too, from the other direction: gate re-applies the grant's ceiling, so a
  decision re-parks on the identical code. The remedies differ by code and
  neither is a tap: a tier park needs wider authority only the operator can
  mint; a cycle park is the stop signal that the review loop ran long — the
  fix is fewer rounds, never a wider grant. flare renders the button; it never
  handles the tap (the callback targets `escalate serve`).
- **Approve is pre-flighted.** The tap is one-shot (`gate judge` cannot be
  re-run), so Approve is withheld when the park's own recorded grant + verdict
  PROVE it cannot land: an expired grant, a verdict tier over the grant's
  ceiling, a spent cycle budget. The card names the blocker and the mint that
  clears it instead. This catches what the ceiling-park rule above cannot — a
  CONTENT park carries no code, so its ceilings are only checked downstream,
  which is how workbench#242 burned a tap. Block always survives (no ceiling
  stops "don't merge this"). Every unreadable fact renders the card flare
  rendered before the check existed: withhold on a proof, never on a gap.
- **A card is corrected, never left stale.** flare records each park card's
  Slack message ref in its journal and closes the card when a terminal artifact
  appears — a judgment or resolution parented to the escalation, a merge
  authorization, or a NEWER park for the same subject (gate's inbox keeps only
  the latest terminal per `repo#PR`, so an older park's buttons resolve
  nothing). Closing strips the buttons, states the outcome, who decided and the
  authorized head sha, and posts the outcome to the card's thread — a
  successful tap and a silently failed one must not look identical.
- **`grant_needed` pages.** gate records a refusal for want of authority
  (`grant_absent` / `grant_expired` / `grant_cycle_exceeded`); flare used to
  drop them. It is the one alert no agent can act on — only the operator mints
  — so it pages early, as its own card class. An absent or expired grant
  carries the paste-ready `gate grant` at the ceilings of the repo's most
  recent merge grant — one grant's tuple, never the max tier of one and the max
  cycles of another. A spent cycle budget carries NO mint: it is the stop
  signal that the review loop ran long, and the fix is fewer rounds, never a
  wider grant. flare proposes what has worked before; it never widens and
  never mints.
- **An identical question collapses, it is never suppressed.** Past two
  deliveries of the same leading reason clause for a repo (7-day window) the
  card names the repeated opener once and leads with what differs — PR, tier,
  head, cycles left — plus the clauses after the opener. Every park still
  pages.
- **A running loop is not a healthy one.** A stalled source (an undeliverable
  event holding an ordered cursor) is recorded in `cursors.json` and makes
  `flare status` report `healthy: false` and `sweep` exit non-zero. A fresh
  `last_poll` alone used to report healthy while nothing got through.
- **Two event classes.** A PAGE routes; an UPDATE applies to a card already
  delivered and is NEVER routed (routing terminal artifacts would page the
  operator for every judgment gate records). An update with no live card
  settles as handled; a closed card is never closed twice; the card index is
  replayed from the journal so a restart strands nothing.- The same resolvable-park rule gates the paste-ready `escalate resolve …`
  context line on the card — including the ceiling exclusion, which gate's
  inbox mirrors. It renders regardless of the channel's button opt-in (it is
  prose, not an interactive element), so the loop closes from a phone with
  Slack and a terminal even before the callback tunnel is up. The line pins
  `-state` to the watched ledger's directory (the watched log's parent): the
  watched path is explicit config, so the pasted command never trusts the
  paster's ambient `$GATE_STATE` to name the same ledger.

## Checks

```
gofmt -l . && go vet ./... && golangci-lint run ./... && go test ./...
```

The sole in-repo dependency is the `contracts` package.
