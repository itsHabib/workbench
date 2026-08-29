# Follow-ups

Tracked here per portfolio convention (status doc, not issues).

## Asks handed to owners (integration edges, not flare work)

1. **ship owner — emit driver parks.** `awaiting_judgment` transitions live
   only in ship's SQLite, which a sink must not read. Ask: append a park
   receipt to `receipts.jsonl` (or an artifact log) at that transition. Until
   then flare's ship coverage is failed/cancelled receipts only; this is the
   remaining push-on-block gap and it is an emission gap.
2. **gate agent — publish the envelope schema.** *Largely resolved:* the
   artifact envelope (`id/kind/run/time/parents/body/prev/hash`) and the
   versioned verdict schema now live in the shared `contracts` package, which
   flare imports — no Go consumer parses either against prose anymore. Open
   remainder: a JSON envelope schema for non-Go readers (ship is TS, dossier is
   Rust), if one is ever needed.
3. **gate agent — structured page signal for ci-classify.** When the
   ci-classify rung lands, carry page-worthiness in a structured field
   (`Finding.severity`) rather than the `infra: <sig> — wants page` title
   prefix. flare will match the title prefix as a stopgap and wants to delete
   that matcher.

4. **gate agent — carry a park's ceilings on the escalation body.** flare
   pre-flights the Approve button by joining the park's `grant` and `verdict`
   ids back to their artifacts; `verdict_tier`, `grant_tier`, `cycles_used` and
   `cycles_max` on the body would turn that join into a read. gate already
   computes all four (`gateResult.CyclesUsed`/`CyclesMax` since #242), so it is
   a write-side field, not new logic. Until then flare consumes them
   defensively: an absent field is an old record and renders as before.

## Parked (with triggers)

- **Narrow the cycle pre-flight's fail-open.** `ledger.cycles` reports "cannot
  say" for EVERY subject if any single outcome in the log has a dangling parent
  verdict — the scan cannot attribute an outcome without the parent it is
  missing, so one bad line disables the cycle check globally. Raised as a P3 on
  #247 and accepted: the direction is safe (uncertainty renders the ordinary
  card, and gate re-applies the ceiling at judgment regardless), and the
  measured join rate on the live ledger is 100% of 332 parks — a fail-open on a
  case that does not currently occur. *Trigger:* a real log grows a dangling
  parent, or the ceilings land on the escalation body (ask #4 above), which
  removes the join entirely.


- **`notified` artifact in State** — trigger: `explain` demonstrably needs
  delivery facts to reconstruct a decision.
- **Neutral drop-dir source kind** — trigger: a producer arrives that has no
  artifact log of its own.
- **Journal rotation/compaction** — trigger: journal replay visibly slows a
  poll (it is one linear scan today).
- **Phone rung** — one webhook URL in config away (ntfy topic); needs no
  code. Operator decision, since it sends event titles off-box.
- **`/health` wiring** — surface `flare status` in the sign-on health board
  so a dead watcher is visible where the operator already looks.
- **`source.Tail` seeks from the end** — trigger: a first-run placement on a
  log large enough that one `os.ReadFile` + full-prefix parse is felt (today
  a 27 MB ledger is ~ms, and every poll already reads the whole file).
- **Empty `hash` at the tail** — `Tail` pins whatever the last record's
  `hash` is; an empty one (a future envelope format) disables the first
  chain check, as a zero cursor already does. Trigger: the gate artifact
  format ever stops sealing every line.
