# Reconciliation brief — ownership, assignment, and what the org metaphor stood in for

> **This document is a handoff prompt.** It is written to be handed to a fresh
> agent with no prior context. Read it top to bottom, then produce the output in
> the last section.
>
> Audience: an analyst with read access to `~/dev`. Modify nothing.
>
> Written 2026-08-24. Every fact below was verified on that date; every one is
> re-checkable, and you should re-check the load-bearing ones — including the
> ones presented as corrections.

---

## 1. The situation

Over roughly six weeks this portfolio produced **ten or more independent
attempts** at some part of *agent ownership* — who owns a unit of work, what
authority they hold, what evidence supports an act, how the next session
inherits the last one's conclusions. Each was built in its own repository, most
were built well, several were measured, and **none shares a vocabulary with the
others**.

The result is a portfolio with three capability models, four evidence journals,
and two implementations of what is probably one idea. A portfolio with three
capability models has zero.

Your job is **not** to design an eleventh thing. It is to make sense of what
exists, name it once, say what should be deleted, and give a build order.

## 2. The governing constraint: the metaphor is not the mechanism

Much of this work has been framed as building an **org** — roles, leads, ICs,
maintainers, reporting lines, a hierarchy. The operator's position, stated
2026-08-24, is that this framing has been doing more harm than work:

> Why does the distinction of an org even matter at all? Previously I was doing
> "driver" and "worker" agents, but it's all the same. Ownership / assignment,
> that's all that matters. Yes, org gives us hierarchy — but that is usually in
> effect to something like permissions or something else, and that can be
> represented any way.

Treat this as the brief's governing constraint, not as one opinion among many.
An org chart is a **proxy** for mechanical properties. Build the properties; the
proxy is optional and probably costs more than it returns at this scale.

**The translation you are asked to work in:**

| org-metaphor term | the property it is actually a proxy for | candidate representations already in the corpus |
|---|---|---|
| role / lead / IC | a **durable assignee identity** that outlives any one session | drive `Scope` + `attach`; `contracts/org` `RoleState`; a dossier project or phase |
| hierarchy | **authority scoping** — who may do what, where | gate grant (repo + tier + TTL); bailiff `Warrant` (target + function + use-cap + expiry); `hack-mandate` delegation; `settings.json` `allow`/`ask`/`deny` layers |
| reporting line | **where a conclusion must land** to be found by the next reader | discharge → dossier task notes; `contracts/org` chain; drive `link` |
| org chart / tree | *possibly nothing.* At 2–5 assignees under one operator the depth is 1, and a depth-1 tree is a list | — |
| headcount / fleet | concurrency limits | — |

**Standing instruction:** wherever this brief, or any source document, uses an
org word, substitute the property and re-ask the question. **If the question
dissolves under substitution, that is a finding — report it.** The most valuable
output of this analysis may be a list of design work that stops being necessary.

Related operator preference, already recorded: prefer stating the property
directly over reaching for borrowed industry shorthand.

## 3. What is settled, what is only evidence

### Settled — verify, then rely on

- **`~/dev/gate` is ARCHIVED.** Last commit: `docs: archive banner — gate
  migrated into workbench as cmd/gate` (2026-07-17). Live code is
  `~/dev/workbench/cmd/gate` (99 Go files, touched 2026-08-23) plus
  `~/dev/workbench/contracts/gateauthorization`. The **only** live thing under
  `~/dev/gate` is `state/` — the `GATE_STATE` hash-chained journal at
  `~/dev/gate/state/log.jsonl`, ~5000 records. Do not read
  `~/dev/gate/{cmd,internal}` as current.
- **switchboard is `~/dev/agents-as-processes-gleam`.** Gate C2 measured that
  process residency does **not** buy crash recovery — a stateless
  reload-from-disk baseline recovers identically, in fewer lines. What residency
  uniquely buys is **serialized ownership**: under two concurrent turns the
  owned form rejects the second, while the unowned baseline lets both write and
  silently corrupts its journal. Measured. Do not re-derive.
- **`hack-branchroom` is the ANCESTOR of `contracts/org`, not a rival.**
  `contracts/org` was ported from it. The dossier task `p1-t3-reduce` in project
  `org` records the three corrections applied during the port — one of which was
  explicitly *"no hardcoded kind vocabulary."* Read that note before treating
  them as duplicates.
- **`braid` and `reprise` are dead.** Both scored 94/100 and won the 2026-08-10
  Haskell bakeoff rounds; both were killed on their own kill conditions
  (`~/dev/bakeoff/haskell-08-10/scorecard.md`,
  `~/dev/bakeoff/haskell-dsl-08-10/scorecard.md`). Do not revive. Do read why
  they died — under-500-lines survives here and they did not.
- **The docs overclaim.** A prior audit found a vision doc's Evidence section
  contradicted by three claims in its own source. Where a document and the code
  disagree, **the code wins, and you say so explicitly.**

### Evidence, NOT a conclusion — this is where an earlier pass overreached

An earlier analysis found that `drive` already implements much of the
durable-assignee property and concluded drive's vocabulary should therefore be
canonical. **That conclusion was not earned and you should not inherit it.** The
underlying observations are real and worth checking:

- `drive/internal/ledger/event.go:36` — `Scope` is an external durable identity
  (`ScopeKindDossierPhase`, `ScopeKindJiraEpic`, `ScopeKindFree`), not a
  per-launch label.
- `drive/internal/verbs/authority.go:13-16` — the current holder is *"the latest
  attacher on its scope. A later re-mint stops [the previous capability] being
  current and its next use reads as revoked, naming the successor. This is also
  orphan adoption: the new current driver inherits authority over [the scope's
  workers]."*
- That reads very close to `contracts/org`'s thesis: one live incarnation, a
  takeover displaces it, the displaced writer's next write is refused, the
  successor inherits held work.

Two implementations that look like one idea is a finding worth confirming. It is
**not** a reason to crown either. `contracts/org` carries verification drive does
not (86 reachable states walked by exhaustive BFS, property tests, fuzz); drive
carries a product surface and real usage `contracts/org` does not. Assess both
on the merits, and consider that the right answer may be neither, or one
absorbing the other, or that they solve subtly different problems.

**A specific crux to settle.** A dossier phase and a Jira epic are **bounded** —
they complete. An area of standing responsibility is not; `agentic-development`
never finishes. Does `ScopeKindFree` carry an unbounded standing scope with the
same durability guarantees, or is it an escape hatch that drops them? Check
`drive/internal/verbs/attach.go`. If unbounded scopes are second-class, that is
a real gap and it is small; if they are first-class, a large amount of proposed
design is redundant.

## 4. Use the instruments before you reason

Measurement beats argument, and instruments exist.

**`~/dev/warrant/cmd/gate-observe`** replays a foreign evidence journal against a
pipeline definition written *afterwards*, requiring no adoption by the observed
system. It is the cheapest way to find out whether a model can account for what
a real pipeline does.

```sh
go run ./cmd/gate-observe ~/dev/gate/state/log.jsonl
```

What it already found against the operator's own merge gate — 415 runs, 5010
records (`~/dev/warrant/docs/gate-history.md`):

- **`escalate` is 57% of all verdicts** — 1218 of 2145, more than `pass` (809)
  and `block` (118) combined. warrant's model had only `supported` and
  `refuted`, so the most common thing a real check says was unrepresentable. A
  third verdict (`insufficient`) was added as a result.
- A separate session reached the same conclusion independently, from the model
  side rather than from real data. Two implementations converging on a missing
  value is the strongest signal in this corpus.
- One verifier's refusal path **has never executed in 415 runs**.

`~/dev/warrant/pipelines/` holds five declared pipelines: `gate`, `gauntlet`,
`maintenance`, `selfcheck`, `shipping`.

**Research already done — do not repeat it:**

| where | what it is |
|---|---|
| `~/dev/agents-as-processes-gleam/docs/evidence/` | 8 pre-registered docs, Gate A → C2, on session residency and ownership |
| `~/dev/agents-as-processes-gleam/docs/research/probe-2-passivation.md` | virtual actors / passivation |
| `~/dev/workbench-laws-lean/` | independent Lean 4 model of a narrow slice of gate's verdict laws, pinned to workbench commit `6eee6aa`. Note its own disclaimer: it proves laws of the *model*, and nothing consumes it to permit a merge |
| `~/dev/bakeoff/agent-substrates-08-21/` | `scorecard.md` and `org-compute-synthesis.md` for the round that produced mandate / obligation / proofline / branchroom |

Three epistemologies — empirical replay, pre-registered experiment, formal proof
— are already aimed at this question. Part of reconciliation is saying which one
settled what.

## 5. Staying alive and rebounding with targeted context

The least-designed property, and the closest to mechanically possible today.

A durable assignee, reduced to mechanism, may be three things:

| property | where it could live | wired today? |
|---|---|---|
| **context** — charter, held work, last conclusions | `CLAUDE.md` layering + a `SessionStart` hook injection | **no** |
| **memory** — what previous incarnations concluded | `contracts/org` chain; drive ledger; discharge records | chain on `main`; discharge unwired |
| **authority** — what it may do unattended | `settings.json` `permissions` layers; gate grant; bailiff `Warrant`; drive scope capability | **no binding to a durable assignee** |

Verified 2026-08-24 in `~/.claude/settings.json`: the `hooks` object contains
**only `PreToolUse` and `PostToolUse`**. There is **no `SessionStart` hook and no
`Stop` hook wired at all.** The Stop hook built in `hooks` PR #42 exists and is
not installed.

So the harness already provides every injection point this needs —
per-directory `CLAUDE.md` layering, `SessionStart` for computed context, layered
`permissions` for scoped authority — and **none is bound to a durable
assignee.** That gap is mechanical, not conceptual.

Assess whether the three-part reduction above is sufficient or a dangerous
simplification. Note that `permissions` layers are static files while a
capability (gate grant, bailiff Warrant) is minted, expiring, and revocable —
those are different security models and the difference probably matters.

Relevant existing work: the `/floor` skill renders the *effective* merged
permission rulebook across global, project and local settings, including how
hooks and wildcards interact. If authority is expressed in settings layers,
`/floor` is what tells the truth about what a layer actually did.

## 6. The corpus

### Candidate implementations

| repo / package | what it implements | last touched |
|---|---|---|
| `workbench/contracts/org` | durable-assignee chain, `Reduce`/`Admissible`, ownership fold; 86 reachable states walked by exhaustive BFS. No hardcoded role vocabulary | on `main` |
| `workbench/cmd/gate` + `contracts/gateauthorization` | merge authorization at an exact head; operator-minted grants, tier ceilings, TTLs; `gate next -json` is its projection | 08-23 |
| `~/dev/drive` | `Scope` / `attach` / `link` / `release`; scope-bound capability, successor re-mint, orphan adoption; liveness derived not written | 08-23 |
| `~/dev/parley` | protocol kernel — Haskell compiles the protocol, Gleam enforces it, Lean proves the two agree | 08-23 |
| `~/dev/warrant` | `Reduce` over an append-only journal; refuses to advance a run without evidence bound to that run's current subject | 08-23 |
| `~/dev/hack-mandate` | signed delegation pinned to one exact task revision / repo / base / head / diff; a child may only shorten | 08-21 |
| `~/dev/hack-obligation` | deterministic evidence–work frontier over frozen verification contracts | 08-21 |
| `~/dev/hack-proofline` | read-only lineage index: which exact identity edge made an old claim | 08-21 |
| `~/dev/hack-branchroom` | rerun as controlled causal fork; epochs, one pure reducer. **Ancestor of `contracts/org`** | 08-21 |
| `~/dev/bailiff` | "the chain as an enforcing bus." A `Warrant` is a capability an agent **holds**: scoped to one target and function, use-capped, wall-clock expiry, operator-revocable | 08-16 |
| `~/dev/agents-as-processes-gleam` | switchboard — residency buys serialized ownership | 08-10 |
| `~/dev/huddle` | per-seat keys as agent identity in a shared room | 07-27 |

### The lifecycle, as currently scattered

- **assign / claim** — `dossier` `task_claim`; the `/claim` and `/release`
  skills over a session-claims log; `drive attach` plus
  `drive/internal/verbs/{authority,write_auth}.go`;
  `drive/internal/reducer/liveness.go` (mtime-quiet past N ⇒ stale).
- **conclude** — `hooks` PR #42: a `Stop` hook appending what a session did to
  the dossier tasks it touched, resolved from the fact that a session which
  called `task_update` named the task in the call. PR #43: a sweep counting
  sessions that owed a discharge and never paid — **measured 18 sessions / 40
  tasks / 0 recorded over 14 days.**
- **notice** — `drive` PR #47: a resident watcher tier. Findings in SQLite, one
  writer, deliberately unable to act.
- **design docs** — `docs/features/org/{vision.md,store-decision.md,p0-findings.md}`
  in this directory; `drive:docs/features/discharge/spec.md` (PR #46).

### A read-path gap

`dossier` `task_list` returns a task's `body` and **omits its notes section**;
no CLI verb reads a note. The MCP's `task_get` does return a structured `notes`
array, but one id per call, walking the whole corpus. So the tier that *writes*
conclusions (bash hooks, the sweep) cannot read them back. Verified 2026-08-24.

## 7. What to work out

1. **Property-by-property, not repo-by-repo.** For each mechanical property in
   §2's table — durable assignee identity, authority scoping, where conclusions
   land — say which implementations provide it, how they differ, and which
   should win. Be specific about **bailiff's Warrant vs gate's grant vs
   hack-mandate's mandate**: three capability models by three different hands.
2. **What dissolves.** Apply §2's standing instruction across
   `docs/features/org/vision.md`. Sections §4.5 ("ownership is a tree;
   dependencies are a graph"), §4.6 ("three things bubble"), and open question
   §10.1 ("what is a fold per node kind" — called *"the real design work behind
   d3"*) are all built on hierarchy. Does any of that survive substitution at a
   depth of 1? Name the design work that stops being necessary.
3. **The lifecycle trace.** Follow one unit of work: assigned → acted on →
   concluded → recorded → inherited. Mark each hop *implemented*, *designed
   only*, or *missing*. The claim/discharge path is the live edge: does claiming
   at session start compose with concluding at session end, and what breaks when
   a session dies between them? Note that #42 deliberately claims at the **end**,
   on the argument that a start-claim is a prediction and agents skip
   predictions — assess whether that holds.
4. **Re-entry (§5).** Is "durable assignee = context bundle + permission set +
   chain position" sufficient or a dangerous simplification? What must a
   `SessionStart` injection contain to be worth its cost, given injected bytes
   enlarge every cached turn thereafter and not just the first?
5. **What to stop maintaining.** Reconciliation means subtraction. Which of
   these should be archived, folded, or deleted, and what is the argument?
6. **Where authority actually stands.** Does `contracts/org` record the
   *authority* for an act, or only that the act happened? Is there an
   implemented effect-class / charter concept, or only a described one? Could a
   durable assignee hold a gate grant or a bailiff Warrant, and what breaks if a
   non-human mints one? `~/.claude/CLAUDE.md` pins minting as operator-only —
   establish whether that is a design necessity or a current convention.

## 8. Framing

**Fully unattended operation is the horizon, not the next step.** Assume
substantial engineering sits in between and do not optimise the answer toward
it. What is needed is a reconciled picture plus the next few moves worth making
regardless of how the autonomy question resolves.

## 9. Constraints

- **Cite `file:line`.** A claim without a citation is noise.
- **Mark every finding `measured` or `reasoned`.**
- **Distinguish IS from SAYS-IT-IS.** Code beats docs; say so when they differ.
- **Crown nothing by default.** Neither drive nor `contracts/org` nor any other
  repo is the answer because it exists or because it is furthest along. An
  earlier pass made exactly that error (§3).
- **No new store, no new repo, no rewrite.** Five stores have already died here,
  each surviving long enough to look like it might still work.
- Measured failure modes in this portfolio — scope errors 43%, stale claims 23%,
  confabulation 8%. Re-verify anything that sounds settled, **including §3.**
- Prefer the cheap local instrument over reasoning where one exists (§4).
- Read-only. Modify nothing.

## 10. Output

At most 2000 words.

- **(a) Property table** — mechanical property · implementations that provide it
  · which should win · why.
- **(b) What dissolves** — design work that stops being necessary once the org
  metaphor is substituted away. Be concrete: name sections and files.
- **(c) Lifecycle trace** — per hop: implemented / designed / missing.
- **(d) Re-entry verdict** — what a `SessionStart` injection must contain, where
  authority lives, whether §5's three-part reduction holds.
- **(e) Subtraction list** — what to archive or fold, with the argument.
- **(f) The next three build steps, in order.** Each with what it makes true,
  why it precedes the others, and its kill condition. Grounded in what exists —
  no greenfield.
- **(g) The single riskiest assumption** in (f).
