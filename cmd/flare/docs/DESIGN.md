# flare — the escalation/block routing sink (v0)

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
| `gate-log` | `~/dev/gate/state/log.jsonl` | envelope `{id, kind, run, time, parents, body, prev, hash}`; events: `kind=escalation` (body `{question, code?, outcome?}` — the notification body, ready-made) and `kind=verdict` with `decision ∈ {block, escalate}` |
| `ship-receipts` | `%APPDATA%/ship/receipts.jsonl` (Roaming, absolute) | receipts with `outcome ∈ {failed, cancelled, parked}`; key = `key` + `outcome`; canonical time = `generated_at` (`terminal_at` accepted for historical rows). A Ship park is failure/dispatch triage, not a Gate policy decision, so it routes at failed severity and never renders a `Your call` headline or Gate resolve actions. |

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
   everything missed — a late toast beats never. A source with **no cursor yet** is placed
   at its current tail first (see *First run* under cursor integrity): catch-up covers the
   gap since flare last looked, not the producer's whole life.
2. **Match.** The source parser decides what is an *event* at all (gate: every escalation,
   every non-pass verdict; ship: failed/cancelled receipts). Every event is push-worthy by
   construction.
3. **Route.** A declarative routes table (see config) picks the channel. An event matching no
   route goes to the **catch-all channel** — absence of a route must not read as "not
   page-worthy". Silence requires an explicit route to the `drop` channel.

   A route selects on `source`, `kind`, `decision`, `outcome`, `code`, `briefed`, and
   `dimension`; every set field must match, and an unset one matches anything. `dimension`
   is the verdict's own `source` — the **ladder rung** that produced it: `reducer` for the
   run-level fold, or a component rung (`readiness`, `review-panel-completeness`,
   `review-consolidation`, …). It exists because step 2 lifts an event per *artifact*, and a
   gate run writes the fold plus every component it folded — with the fold restating the
   worst component's `why` verbatim. Selecting on `decision` alone therefore pages one card
   per rung for a single decision. Match `dimension: "reducer"` to page the run once and
   drop its parts.

   Escalate-folds need no route at all: a parked run is announced by its **escalation**
   artifact, which carries the brief, the resolve actions, and a `ts` flare can go back and
   close. That asymmetry is load-bearing — only escalation cards enter the card index
   (`cardOf`), so a verdict card is fire-and-forget and can never be corrected. A redundant
   one misreports a resolved park as live for as long as the channel scrolls. Nor can
   dropping escalate-folds hide a park: the escalation artifact is what *creates* the park
   in gate's inbox, so a run without one has no park to hide (`gate next` is empty too, and
   the caller saw the run die rather than park).
4. **Dedupe/throttle.** Dedupe on artifact ID (gate) / `key+outcome` (ship): a
   restart-and-resweep never re-pages. Throttle is per-route (min seconds between pushes for
   the same route) and **severity-monotone**: a strictly worse event (block > escalate >
   failed > cancelled) passes through an open throttle window — worst wins, the reducer's
   monotone spirit applied to notifications.

## What reaches the phone

Three rules beyond "every event is push-worthy", each answering a way the phone rung
misinformed the operator in production.

- **`grant_needed` is its own page class.** gate records a `grant_needed` artifact whenever a
  run is refused for want of authority — `grant_absent`, `grant_expired`, or (since gate #242)
  the pre-flight `grant_cycle_exceeded`. flare read those artifacts and **dropped them**, and
  they are the ONE alert an agent cannot act on for itself: it can re-run, re-review and
  re-judge, but it cannot mint, and the operator cannot mint from a phone either. So the page
  must arrive *early*, while they are still near a keyboard, rather than being discovered later
  as stalled work. An absent or expired grant's card carries the paste-ready `gate grant` with
  the ceilings of the repo's most recent merge grant — one grant's tuple, never a composite of
  several — so flare proposes what the operator has already judged appropriate and never widens
  on its own. A spent cycle budget carries no mint at all: the ceiling is the stop signal that
  the review loop ran long, and the remedy is fewer rounds, not a wider grant.

- **`flare digest` — the standing authority picture.** The per-event pages answer one refusal
  at a time; the digest answers all of them at once: per repo, how much is parked, whether a
  grant is standing, and how soon it lapses. Two situations qualify, and only a mint fixes
  either: parked work with **no live grant** (a hard stop), and a live grant about to lapse
  under parked work (a stop that is coming). A repo running fine is left out — a digest that
  lists everything is another wall of text to skim, and no pressure at all produces no card.
  Its dedupe id is a hash of its own content, so an unchanged picture never re-pages and any
  movement in it does.

- **An identical question collapses.** 318 of 355 parks in the live ledger lead with the
  identical readiness sentence, and attention to a repeated warning is spent by the second one
  — so the third onward buys nothing by restating it, while what is DIFFERENT about this park
  is never surfaced at all. flare fingerprints the park's **leading reason clause** (gate packs
  reasons into one `"; "`-joined line whose first clause is the primary one; fingerprinting the
  whole line matches almost never — 26 of 357 — even though the operator is reading the same
  opening sentence every time), counts deliveries of it per repo over a 7-day window, and from
  the third collapses the card: it names the repeated opener once, then leads with the PR, the
  tier, the head the verdict was gathered against and the review budget left, and shows the
  clauses AFTER the opener — the part that is actually new. **Measured on the live ledger: 276
  of 357 parks (77%) would render collapsed; nothing is suppressed — the collapse changes the
  card, never whether it is sent.**

## Cursor integrity (absence must not read as calm)

- **First run (no cursor yet):** a source absent from `cursors.json` — fresh state on a new
  machine, or a source newly added to the config — is **placed at its current tail**: offset
  = the end of the last complete line, `last_hash` = that record's hash (gate-log). The
  placement is journaled as one `cursor-init` entry and logged once; nothing before it is
  delivered. A producer's history is not a page queue: replaying a 4k-line gate ledger into
  Slack pages every long-dead escalation (with live Approve/Block buttons) until the API
  rate-limits, and tells the operator nothing they can act on. A file that does not exist
  yet places at offset 0 — everything that appears is new. **`flare sweep -from-start`**
  (also accepted by `watch`) is the deliberate opt-in to deliver the whole history; dedupe
  holds on every later sweep as usual. The flag only affects sources with no cursor; it
  never moves an existing one.
- **Absent ≠ reset.** "No cursor" (fresh) and "cursor at offset 0" (a deliberate resweep) are
  different facts and must never be confused: the resweeps below write an explicit zero
  cursor, so recovery is never mistaken for a first run and placed at the tail — which would
  silently skip exactly what the break hid.
- **gate-log:** the cursor stores byte offset + the hash of the last processed line. On poll,
  the first new line's `prev` must equal the stored hash; the file shrinking below the offset
  means truncation. Either mismatch **fires a flare itself** (`cursor-alert`) and resweeps
  from zero (dedupe prevents re-paging) — never a silent reset.
- **ship-receipts:** offset only (no chain); shrink → same alert + resweep.
- **cursors.json corrupt:** a cursor *file* that exists but does not parse is recoverable, not
  fatal. The cycle quarantines it aside (`cursors.json.corrupt-<nanos>`, kept for forensics),
  fires a `cursor-alert`, and resweeps every configured source from an explicit offset 0
  (dedupe prevents re-paging) — the same "never a silent reset" contract as a chain
  mismatch, extended so a corrupt file can never silently *wedge* the loop either. Writes are torn-proof: each save renders to a unique
  `os.CreateTemp` file and renames, so no two writers share a temp to interleave into.
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
  - **Resolve actions (opt-in).** When a slack channel sets `"resolve_actions": true`, a
    *resolvable* parked escalation (a gate park — event kind `escalation` — carrying its
    artifact id **and the grant it ran under**) additionally renders **Approve** and **Block** interactive buttons beside the
    `View PR` link. Each button carries the shared `contracts/escalation` action-id vocabulary
    (`ActionApprove` / `ActionBlock`) and the escalation artifact id as its value, so a signed
    Slack callback resolves the right park with nothing pasted. flare only *paints* the buttons —
    the tap is handled by `escalate serve`, never flare (Amendment 3). The toggle is **off by
    default and deliberately so**: the buttons only work once the Slack app's interactivity
    Request URL is pointed at a running `escalate serve` (behind a tunnel), and a rendered button
    with no configured Request URL is a dead tap. Turning it on is the operator's signal that the
    ingress is wired. Buttons never render for the other things that reach `SevEscalate` (a
    verdict with an escalate decision, a cursor-alert, a park missing its id, or a grantless
    park — including one carrying the documented `none:`-prefixed sentinel, which lifts as
    grantless) — those are not resolvable through `escalate`, so offering Approve/Block on
    them would be a tap `gate resolve` would refuse; a grantless park resolves out-of-band in
    the producer's own flow.
  - **Pre-flighting the Approve button.** A tap is **one-shot** — `gate judge` records a
    judgment that cannot be re-run — so a button offered on a decision that is already
    guaranteed to fail *burns* the operator's approval. On 2026-08-21 a tap on workbench#242
    did exactly that: it recorded a judgment and then died on `grant_tier_exceeded: verdict
    tier T3 exceeds grant ceiling T1`. The park was a **content** park (no code), so the
    `ceilingPark` exclusion above — which reads the park's own code — did not apply; the
    ceiling was only checked downstream, after the judgment was spent.

    Before rendering **Approve**, flare now joins the park to the two artifacts it names by id
    — the grant it ran under and the reduced verdict it stands on — and applies the same
    ceilings gate applies, in gate's order: **expiry, then the tier ceiling, then the
    review-cycle ceiling** (`internal/preflight`). When any of them proves the approval cannot
    land, the card renders **without** Approve and states the blocker, the authority that
    would clear it, and a paste-ready `gate grant` line. **Block still renders** — no ceiling
    stops a human deciding "don't merge this", and the operator away from a keyboard should
    still be able to say so.

    This is flare **rendering a fact gate already recorded**, not flare gating: it decides
    nothing, writes nothing, and cannot change any outcome. The failure direction is
    deliberately asymmetric — a missing grant, an unknown tier, an underivable cycle count all
    resolve to *unknown*, and an unknown renders exactly the card flare rendered before the
    check existed. **A button is withheld only on a proof.** Withholding one the operator
    needed is the worse failure, because the phone is the only surface they have away from a
    keyboard.

    The joins are read-only over the same log flare already tails (`internal/source/ledger.go`,
    lazily built, so a poll with no park pays nothing). Two things the escalation artifact does
    **not** carry make this a join rather than a read: the grant's ceilings and the verdict's
    tier. An additive `ceilings: {verdict_tier, grant_tier, cycles_used, cycles_max}` on the
    escalation body would make it a read — noted for gate in *Integration edges* below.
  - **Card lifecycle: a card must reflect the park's current state.** A card is a snapshot,
    and a snapshot of a parked escalation is stale the moment the park resolves. gate's inbox
    reduces parked runs **by subject** — only the latest terminal artifact per `repo#PR` is
    still parked — so a re-park, a merge, or a keyboard `gate judge` silently drops an older
    park out of the inbox while its Approve button stays live in Slack. Tapping one then fails
    with `escalation is not currently parked: esc_… not in gate inbox`, which reads as a broken
    tool rather than a card that moved on. Two taps died that way on 2026-08-21.

    flare records the message ref (`channel`+`ts`) of every park card it posts, in its own
    journal, and closes that card when a terminal fact appears in the log:

    | artifact | closes the card as |
    |---|---|
    | `judgment` parented to the escalation | approved / blocked, by whoever decided |
    | `resolution` parented to the escalation | same, with the **verified** Slack identity |
    | `action` with `would_merge` | merge authorized, with the pinned `--match-head-commit` sha |
    | a **newer escalation for the same subject** | superseded — closed *before* the new card posts |

    Closing a card removes the resolve buttons, states the outcome and who decided, and posts
    the outcome as a **thread reply** — because today a successful tap and a silently failed
    one look identical once the ack fades. The sha is labeled *authorized*, never *merged*:
    gate's action records the merge command and the exact head it pins, but the merge is run by
    the caller and gate keeps no receipt that one landed.

    These terminal artifacts are a second **event class** (`ClassUpdate`): they are APPLIED to
    a card, never routed. Routing them would page the operator for every judgment gate records
    (237 in the live ledger). An update with no live card settles as handled — flare may have
    started after the park was paged. A card already closed is not closed again; the index is
    replayed from the journal, so a restart does not strand live cards.

    Still a sink: every fact above was written by gate, and correcting a card writes nothing
    anywhere but Slack and flare's own journal.
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

- `~/.flare/journal.jsonl` — append-only `{time, kind: delivered|dropped|skipped-throttle|
  cursor-alert|cursor-init|error, source, event_id, channel, note}`. Answers "was the
  operator paged at T" — and, via `cursor-init`, "where did flare first look, and what did
  it deliberately not deliver". flare explains delivery; producers explain decisions.
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
    {"name": "gate", "kind": "gate-log", "path": "C:/Users/<you>/dev/gate/state/log.jsonl"},
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
- `flare sweep` — one catch-up pass, then exit. Exit 0 = swept clean; 1 = config/source
  error.
- `-from-start` (`sweep`/`watch`) — a source with no cursor yet starts at offset 0 and
  delivers its whole history instead of being placed at the tail. Opt-in, once; an
  existing cursor is never moved by it.
- `flare status` — JSON health (last poll, per-source cursor, journal tail). Exit 0 healthy,
  1 stale/never-ran/**stalled**, 2 config error. A corrupt cursor file reports `healthy:false` +
  `cursors_corrupt:true` and exits 1 (not a raw parse error) — the watcher is down, and
  `/health` must be able to see why.
  - **A running loop is not a healthy one.** A fresh `last_poll` proves the loop is *running*;
    it does not prove anything is getting through. The cursor is ordered, so one undeliverable
    event blocks every event behind it on that source — and that used to report
    `healthy: true` while `sweep` exited 0. A stalled source is now recorded in
    `cursors.json` (`stalled`: when it began, what it is stuck on, how many attempts) and
    `status` reports `healthy: false` and names it. A source that polls cleanly clears its own
    stall.
- `flare digest [-within 12h]` — one authority card: repos with parked work and no live grant,
  and grants lapsing within the window with work behind them, each with its mint. Exit 0 when
  delivered or when there was nothing to say, 1 on a read/delivery failure, 3 if another flare
  holds the lock (it journals, so it takes the same single-instance lock).

**Single-instance (`watch` + `sweep`).** Both take an exclusive OS advisory lock on
`~/.flare/watch.lock` (flock on Unix, `LockFileEx` on Windows) before touching state, and
exit **3** if another flare already holds it. Two writers into the journal + cursors — a
second watcher, or a manual `sweep` racing a running watcher — is what corrupted
`cursors.json` (each side rename-replacing a shared temp with interleaved bytes); the lock
makes that unrepresentable. The lock is process-lifetime and OS-released on crash (no stale
lock to reap). `status` never locks — it only reads.

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
2. **gate agent (additive, would turn a join into a read):** carry the park's **ceilings** on
   the escalation body — `verdict_tier`, `grant_tier`, `cycles_used`, `cycles_max`. flare
   pre-flights the Approve button today by joining the park's `grant` and `verdict` ids back
   to their artifacts in the log; those four values are what the join is for. gate already
   computes all of them (`gateResult.CyclesUsed`/`CyclesMax` since #242), so this is a
   write-side field, not new logic — and it would let a reader with only the escalation line
   decide, instead of one holding the whole log. Until then flare consumes them defensively:
   an absent field is an old record and renders as before.
3. **gate agent:** (a) publish the artifact *envelope* schema next to `verdict-schema-v0.json`
   so external readers stop parsing against prose; (b) when ci-classify's `infra` findings
   land, carry page-worthiness in a structured field (`Finding.severity` exists) rather than
   the `infra: <sig> — wants page` title prefix — flare will match on the title prefix as a
   v0 stopgap and wants to delete that matcher.
