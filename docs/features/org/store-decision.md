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

**2. Notes are readable only one task at a time, and only by an LLM.**
`task_update` appends to a task's `## Notes` section. Three separate facts,
each verified 2026-08-23:

- `task_list` returns a task's `body` and **omits `## Notes` entirely** —
  diffed `dossier task_list --project org` against the on-disk task file: known
  note text present on disk, absent from every field returned.
- The **CLI** has no verb that returns notes at all. `dossier --help` lists
  `serve`, `task_complete`, `task_update`, `artifact_link`, `task_list`,
  `artifact_list`. Nothing there reads a note.
- The **MCP** does have one: `task_get` returns a structured `notes` array
  (`actor`, `body`, `posted_at`). It takes a single id and, by its own
  description, *"walks the whole corpus"* to find it.

So the read path exists, but only for an LLM holding an MCP connection, one
task per call, at O(corpus) each. The Stop hook is bash and the sweep is bash;
neither can reach it. That is why `scripts/discharge-sweep.sh` greps the corpus
markdown, and why that access is isolated in one function.

Discharge §4.1 says *the reader is the next agent, and the read ships first.*
The next agent can in fact read — one task at a time, by full corpus walk. It
is the writing tier that is blind, and the cost of a read scales with the
corpus rather than with the answer.

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

The role lead's SessionStart injection is that consumer, and it fails on
exactly the two stated terms: dossier verbs **cannot** serve it (finding 2 —
the conclusions are unreadable through the API), and an LLM doing its own
retrieval **cannot** meet the 400 ms budget. The condition is met as written,
not reinterpreted.

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

- **If notes turn out to be readable** through some verb this survey missed,
  finding 2 collapses and the pressure drops from "cannot" to "slow". The
  substrate argument then rests on findings 3 and 4 alone — still sufficient
  for the chain, no longer urgent for discharge.
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
