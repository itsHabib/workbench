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

## Parked (with triggers)

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
