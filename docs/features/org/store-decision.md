# The store decision

> Status: decided on engineering grounds, 2026-08-23. Reverses nothing in
> [`vision.md`](vision.md); narrows `drive:docs/features/discharge/spec.md` §4.2.
> Read this before building anything that writes.

## The question

Two statements are on the table and they look contradictory.

- Discharge spec §4.2: **"No new store; dossier holds it."** Four dead stores
  are four data models nobody reconciles; dossier is the one still alive.
- The operator, 2026-08-23: **back this with a real database, not files**, and
  add **watcher processes**.

## The answer

They are not contradictory, because they answer different questions.

§4.2's argument, stated precisely, is about **schema duplication**: do not
invent a fifth model of projects/phases/tasks/conclusions beside dossier's,
because nobody reconciles five models. That argument is correct and this
decision does not weaken it. But it is an argument about the *model*, and it
never examined the *substrate* — markdown-on-disk was inherited, not chosen.

The operator's direction is about the substrate.

Both hold, and together they say one thing:

> **One model. On a real database. The database absorbs dossier's model rather
> than sitting beside it.**

A sixth store would be a sixth *model*. Giving the surviving model a spine that
can answer questions is the opposite move.

## Why the current substrate cannot carry what is already built

Four findings, each checkable.

**1. The corpus is re-parsed per call.** dossier's own README: the corpus is
"plain markdown you can grep and edit by hand; the server re-reads it on every
call." 5.4 MB at `~/dev/dossier-state`, today.

**2. ~~Notes are unreadable.~~ WITHDRAWN — the premise was false.**

This finding twice claimed that conclusions written by `task_update` could not
be read back: first that no CLI or MCP path returned them, then, narrowed, that
only the MCP's `task_get` did, one id per call.

**Both versions are wrong.** `dossier task_list` returns a structured `notes`
array (`actor`, `body`, `posted_at`) for every task, in one call.

The error is worth recording because it is a measurement error, not a reasoning
one, and it survived two rounds of "verification". `Task.notes` is
`#[serde(default, skip_serializing_if = "Vec::is_empty")]`
(`~/dev/dossier/src/domain.rs:184`), so the key is **absent** from any task with
no notes. The check that produced the finding ran `jq '.[0] | keys'` over a task
list, saw no `notes` key on that one row, and generalised. Most rows have no
notes. A conditionally-serialised field and an absent field are
indistinguishable from a single sample.

Refuted by a later analysis; confirmed here 2026-08-24 by querying a task known
to carry notes (`org/p1-t3-reduce` → `has_notes_key: true, note_count: 3`).

**What this costs the argument.** `hooks` PR #43 justified reading the corpus
markdown directly on this premise; that code now reads through the CLI instead,
and got 5x faster doing it. Discharge §4.1's *"the reader is the next agent, and
the read ships first"* is in better shape than this document claimed — the read
path exists at every tier, including bash.

**What survives.** Nothing in the decision rests on this finding. Findings 1, 3
and 4 are independent, and finding 4 alone is sufficient: a compare-and-swap has
nowhere to live on markdown files. The substrate argument stands on a narrower
base than it was written with, and should be read that way.

**3. It cannot be watched.** A watcher's whole question is *what changed since
X*. A markdown tree answers that with a filesystem walk plus a re-parse. This
is why watcher processes were never on the table before — not because nobody
wanted them.

**4. There is nowhere correct to put the chain.** This is the one that decides
it. `vision.md` §2 T1 says ownership and continuity are the same fact and *the
fact is a compare-and-swap*. A CAS is an atomic conditional write. Markdown
files do not have one. `contracts/org` is on `main` — spine, contract law,
ownership fold, all 86 reachable states walked — and it has no store that can
hold it without reintroducing by convention the exact race the fold refuses.

**The thing already built has no substrate that can hold it correctly.** That
is the decision, and it was already true before the operator asked.

## The engine: SQLite now, Postgres-compatible schema, behind one interface

Not Neon/Postgres today:

- The Neon MCP is configured and **unauthorized**; adopting it means starting
  with an OAuth step. §4.3's finding is that procedural steps are exactly what
  gets skipped — and a store that needs one before it works is the sixth dead
  store with extra latency.
- It puts a network round-trip inside the SessionStart injection path, which
  GATE A caps at **<400 ms p95**.
- It bills money to serve one Mac.
- Nothing in the design has a second writer host yet.

SQLite is not the timid option here. The toy properties are *markdown's*, and
SQLite answers each one directly: transactions, foreign keys, `UNIQUE`
constraints (which is what makes the CAS a CAS), indexes, WAL concurrency, and
a `WHERE updated_at > ?` that turns a watcher into a goroutine instead of a
directory walk.

**When Postgres.** The first time a writer that is not this Mac needs to append
— a cloud `drive` session discharging, or a second machine. That is a driver
swap and not a rewrite **only if the schema stays inside the common subset from
the first migration**, so it does: no SQLite-only types, no `AUTOINCREMENT`, no
Postgres-only `RETURNING` in the store interface.

## cortex unpauses, on its own stated terms

`prj_01KRT2XJ1P3SRSKQJSNV7WTY1Z` — *"cortex — agentic context engine
(paused)"* — was paused 2026-05-17 with an explicit revival condition:

> revisit when there's a real consumer that can't be served by dossier verbs +
> an LLM doing its own retrieval.

The SessionStart injection is the candidate consumer. **With finding 2
withdrawn, this condition is no longer clearly met**, and the honest reading is
weaker than the one first written here.

The condition has two terms. "Cannot be served by dossier verbs" is now **false**
— `task_list` returns notes, so the verbs do serve retrieval. Only the second
term survives: whether an LLM doing its own retrieval can meet the <400 ms p95
injection budget against a 5.4 MB corpus that is re-parsed per call. That is a
latency question, it is measurable, and **it has not been measured.**

So cortex does not unpause on the strength of this document. It unpauses if
someone measures the injection path and it misses the budget. Until then the
revival condition stands unmet, which is what its own wording asks for.

## What this does not authorize

- **Not a rewrite of dossier.** Its model, its MCP surface, and its verb names
  survive. The substrate underneath them changes, and one migration reads the
  markdown tree into rows.
- **Not a new repo yet.** Where the store lives (inside dossier, inside drive,
  or as cortex revived) is a separate call that should follow the first
  schema, not precede it.
- **Not P2–P7 of `vision.md`.** The §7 P0 gate is unchanged by this. This
  decision is about where the already-built P1 lives.
- **Not the watchers yet.** Watchers are the *reason* for the substrate, but
  the first one should be written against a schema that exists.

## How this could be wrong

- **~~If notes turn out to be readable~~ — this happened.** The original text
  read: *"If notes turn out to be readable through some verb this survey missed,
  finding 2 collapses and the pressure drops from 'cannot' to 'slow'. The
  substrate argument then rests on findings 3 and 4 alone — still sufficient for
  the chain, no longer urgent for discharge."* That is exactly what occurred,
  within a day. The prediction was right and so was the consequence: the chain
  argument holds, the discharge urgency does not. Kept visible rather than
  deleted — a falsification clause that fires is the most useful line in a
  design document.
- **If the migration is where corpora go to die**, the honest evidence is that
  five stores already died and none of them died of a bad *engine*. A migration
  that loses the operator's 5.4 MB of project memory would be the first new
  failure mode this decision introduces. It must be reversible: the markdown
  tree stays on disk, read-only, until a full round-trip is proven.
- **If SQLite's single writer binds sooner than expected** — several concurrent
  sessions all discharging — WAL handles it to a point and then does not. The
  measurement to watch is write contention, not read latency.

## Sequencing

1. Schema first, in the common subset, with the markdown tree as the source of
   truth for a reversible one-way migration.
2. `discharge-sweep`'s `discharge_recorded()` is the canary: it is the one
   function coupled to the substrate, and swapping it to a query is how the
   first read gets proven end-to-end.
3. One watcher, against a schema that exists — not before.
