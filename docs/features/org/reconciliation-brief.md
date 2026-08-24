# Reconciliation brief — the org loop

> **This document is a handoff prompt.** It is written to be handed to a fresh
> agent with no prior context. Read it top to bottom, then produce the output in
> the last section.
>
> Audience: an analyst with read access to `~/dev`. Modify nothing.
>
> Written 2026-08-24. Every fact below was verified on that date; every one is
> re-checkable, and you should re-check the load-bearing ones.

---

## 1. The situation

Over roughly six weeks, this portfolio produced **ten or more independent
attempts** at some part of *agent ownership* — who owns a unit of work, what
authority they hold, what evidence supports an act, how the next session inherits
the last one's conclusions. Each was built in its own repository, most were
built well, several were measured, and **none shares a vocabulary with the
others**.

The result is a portfolio with three capability models, four evidence journals,
and two reducers that are secretly the same reducer. A portfolio with three
capability models has zero.

Your job is **not** to design an eleventh thing. It is to make sense of what
exists, name it once, say what should be deleted, and give a build order.

## 2. The vision being reconciled toward: an org loop

One sentence: **the next agent starts where the last one stopped, and two agents
do not silently reach different conclusions about the same thing.**

Concretely, the operator wants **two to five role leads** — not a seventy-five
agent fleet. `lead:agentic-development` owns the portfolio's own tooling;
`lead:rooms` owns rooms. Each one:

1. **owns an area** and outlives any session working in it,
2. **holds work** you can enumerate,
3. **stays alive** — a fresh session inherits its judgment rather than
   rediscovering it,
4. **rebounds with targeted context** — a session opening under a lead boots
   with that lead's charter, its held tasks, what the last incarnation
   concluded, what it left open, and an authority scoped to its area,
5. **reports back** without being asked.

Item 4 is the one that has had the least design attention and is closest to
being mechanically possible today. See §5.

## 3. Ground truth corrections — earlier analyses got these wrong

Verify these first. Each has already caused a wrong conclusion.

- **`~/dev/gate` is ARCHIVED.** Its last commit is literally
  `docs: archive banner — gate migrated into workbench as cmd/gate`
  (2026-07-17). The live code is `~/dev/workbench/cmd/gate` (99 Go files, last
  touched 2026-08-23) plus `~/dev/workbench/contracts/gateauthorization`. The
  **only** live thing under `~/dev/gate` is `state/` — the `GATE_STATE`
  hash-chained journal at `~/dev/gate/state/log.jsonl`, ~5000 records. Do not
  read `~/dev/gate/{cmd,internal}` as current.
- **switchboard is `~/dev/agents-as-processes-gleam`.** Its Gate C2 result:
  process residency does **not** buy crash recovery — a stateless
  reload-from-disk baseline recovers identically, in fewer lines. What residency
  uniquely buys is **serialized ownership**: under two concurrent turns the
  owned form rejects the second, while the unowned baseline lets both write and
  silently corrupts its journal. This is measured. Do not re-derive it.
- **`hack-branchroom` is the ANCESTOR of `contracts/org`, not a rival.**
  `contracts/org` was ported from it. The dossier task `p1-t3-reduce` in project
  `org` records the three corrections applied during the port — read that note
  before treating them as duplicates.
- **`braid` and `reprise` are dead.** Both scored 94/100 and won the 2026-08-10
  Haskell bakeoff rounds; both were then killed on their own kill conditions.
  See `~/dev/bakeoff/haskell-08-10/scorecard.md` and
  `~/dev/bakeoff/haskell-dsl-08-10/scorecard.md`. Do not revive them. Do read
  why they died — under-500-lines survives here and they did not.
- **The docs overclaim.** A prior audit found a vision doc's Evidence section
  contradicted by three claims in its own source. Where a document and the code
  disagree, **the code wins, and you say so explicitly.**

## 4. Use the instruments before you reason

Measurement beats argument, and instruments already exist.

**`~/dev/warrant/cmd/gate-observe`** replays a foreign evidence journal against a
pipeline definition written *afterwards*, requiring no adoption by the observed
system. It is the cheapest way to find out whether a model can account for what a
real pipeline does.

```sh
go run ./cmd/gate-observe ~/dev/gate/state/log.jsonl
```

What it already found, against the operator's own merge gate (415 runs, 5010
records — `~/dev/warrant/docs/gate-history.md`):

- **`escalate` is 57% of all verdicts** — 1218 of 2145, more than `pass` (809)
  and `block` (118) combined. warrant's model had only `supported` and
  `refuted`, so the single most common thing a real check says was
  unrepresentable. A third verdict (`insufficient`) was added as a result.
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

Three different epistemologies — empirical replay, pre-registered experiment,
formal proof — are already aimed at this question. Part of reconciliation is
saying which one settled what.

## 5. The re-entry axis: staying alive and rebounding with targeted context

This is the least-designed part of the vision and the closest to buildable, so
it gets its own section.

A role lead, reduced to mechanism, is three things:

| what a lead needs | where it would live | wired today? |
|---|---|---|
| **context** — charter, held work, last conclusions | `CLAUDE.md` layering + a `SessionStart` hook injection | **no** |
| **memory** — what previous incarnations concluded | `contracts/org` chain + discharge records | chain on `main`; discharge unwired |
| **authority** — what it may do unattended | `settings.json` `permissions` layers (`allow`/`ask`/`deny`) + a gate grant or bailiff Warrant | **no role binding** |

Verified 2026-08-24 in `~/.claude/settings.json`: the `hooks` object contains
**only `PreToolUse` and `PostToolUse`**. There is **no `SessionStart` hook and
no `Stop` hook wired at all**. The Stop hook built in `hooks` PR #42 exists and
is not installed.

So: the harness already provides every injection point this vision needs —
per-directory `CLAUDE.md` layering, `SessionStart` for computed context,
layered `permissions` for scoped authority — and **not one of them is bound to a
role.** That gap is mechanical, not conceptual, and it is worth assessing
whether "a role lead is a context bundle + a permission set + a chain position"
is the whole of it or a dangerous simplification.

Relevant existing work: the `/floor` skill renders the *effective* merged
permission rulebook across global, project, and local settings, including how
hooks and wildcards interact. If per-session authority is going to be expressed
in settings layers, `/floor` is the tool that says what a layer actually did.

## 6. The corpus

### Ownership / authority / evidence primitives

| repo / package | the primitive it invented | last touched |
|---|---|---|
| `workbench/contracts/org` | role chain, `Reduce`/`Admissible`, ownership fold; 86 reachable states walked by exhaustive BFS | on `main` |
| `workbench/cmd/gate` + `contracts/gateauthorization` | merge authorization at an exact head; operator-minted grants, tier ceilings, TTLs; `gate next -json` is its projection | 08-23 |
| `~/dev/parley` | protocol kernel — Haskell compiles the protocol, Gleam enforces it, Lean proves the two agree | 08-23 |
| `~/dev/warrant` | `Reduce` over an append-only journal; refuses to advance a run without evidence bound to that run's current subject | 08-23 |
| `~/dev/hack-mandate` | signed delegation pinned to one exact task revision / repo / base / head / diff; a child mandate may only shorten | 08-21 |
| `~/dev/hack-obligation` | deterministic evidence–work frontier over frozen verification contracts | 08-21 |
| `~/dev/hack-proofline` | read-only lineage index: which exact identity edge made an old claim | 08-21 |
| `~/dev/hack-branchroom` | rerun as controlled causal fork; epochs, one pure reducer. **Ancestor of `contracts/org`** | 08-21 |
| `~/dev/bailiff` | "the chain as an enforcing bus." A `Warrant` is a capability an agent **holds**: scoped to one target and one function, capped at N uses, wall-clock expiry, operator-revocable | 08-16 |
| `~/dev/agents-as-processes-gleam` | switchboard — residency buys serialized ownership | 08-10 |
| `~/dev/huddle` | per-seat keys as agent identity in a shared room | 07-27 |

### The ownership lifecycle, as currently scattered

- **claim** — `dossier` `task_claim`; the `/claim` and `/release` skills over a
  session-claims log; `drive attach` plus
  `drive/internal/verbs/{authority,write_auth}.go`;
  `drive/internal/reducer/liveness.go` (mtime-quiet past N ⇒ stale).
- **conclude** — `hooks` PR #42: a `Stop` hook appending what a session did to
  the dossier tasks it touched, keyed off the fact that a session which called
  `task_update` named the task in the call. PR #43: a sweep counting sessions
  that owed a discharge and never paid — **measured 18 sessions / 40 tasks / 0
  recorded over 14 days.**
- **notice** — `drive` PR #47: a resident watcher tier. Findings in SQLite, one
  writer, deliberately unable to act.
- **design docs** — `docs/features/org/{vision.md,store-decision.md,p0-findings.md}`
  in this directory; `drive:docs/features/discharge/spec.md` (PR #46).

### A note on read paths

`dossier` `task_list` returns a task's `body` and **omits its notes section**;
no CLI verb reads a note. The MCP's `task_get` does return a structured `notes`
array, but one id per call, walking the whole corpus. So the tier that *writes*
conclusions (bash hooks, the sweep) cannot read them back. Verified 2026-08-24.

## 7. What to work out

1. **One vocabulary.** For each genuinely distinct primitive: one canonical
   name, which repos implement it, and whether they are aliases or rivals. Be
   specific about **`bailiff`'s Warrant vs gate's grant vs `hack-mandate`'s
   mandate** — three capability models by three different hands. Say which one
   should win and why.

2. **The lifecycle trace.** Follow one unit of work: claimed → acted on →
   concluded → recorded → inherited. At each hop mark *implemented*, *designed
   only*, or *missing*. The claim/discharge path is the live edge: assess
   whether claiming at session start and concluding at session end actually
   compose, and what breaks when a session dies between them. Note that #42
   deliberately claims at the **end**, on the argument that a start-claim is a
   prediction and agents skip predictions — assess whether that holds.

3. **The re-entry mechanism (§5).** Is "a lead = context bundle + permission set
   + chain position" sufficient? What does a `SessionStart` injection have to
   contain to be worth its cost, given that injected bytes enlarge every cached
   turn thereafter and not just the first? Where should a role's authority live
   so that `/floor` can still tell the truth about it?

4. **What to stop maintaining.** Reconciliation means subtraction. Which of
   these should be archived, folded into another, or deleted — and what is the
   argument in each case?

5. **Where authority actually stands.** Does `contracts/org` record the
   *authority* for an act, or only that the act happened? Is there an
   implemented effect-class / charter concept, or only a described one? Could a
   role lead hold a gate grant or a bailiff Warrant, and what breaks if a
   non-human mints one? `~/.claude/CLAUDE.md` pins minting as operator-only —
   establish whether that is a design necessity or a current convention.

## 8. Framing

**Fully unattended operation is the horizon, not the next step.** Assume
substantial engineering sits in between and do not optimise the answer toward
it. What is needed is a reconciled picture plus the next few moves that are
worth making regardless of how the autonomy question resolves.

## 9. Constraints

- **Cite `file:line`.** A claim without a citation is noise.
- **Mark every finding `measured` or `reasoned`.**
- **Distinguish IS from SAYS-IT-IS.** Code beats docs; say so when they differ.
- **No new store, no new repo, no rewrite.** Five stores have already died here,
  each surviving long enough to look like it might still work.
- Measured failure modes in this portfolio — scope errors 43%, stale claims 23%,
  confabulation 8%. Re-verify anything that sounds already settled, **including
  the corrections in §3.**
- Prefer the cheap local instrument over reasoning where one exists (§4).
- Read-only. Modify nothing.

## 10. Output

At most 2000 words.

- **(a) Vocabulary table** — canonical primitive · implementations · alias or
  rival · which wins.
- **(b) Lifecycle trace** — per hop: implemented / designed / missing.
- **(c) Re-entry verdict** — what a `SessionStart` injection must contain, where
  a role's authority lives, and whether the three-part reduction in §5 holds.
- **(d) Subtraction list** — what to archive or fold, with the argument.
- **(e) The next three build steps, in order.** Each with: what it makes true,
  why it precedes the others, and its kill condition. Grounded in what exists —
  no greenfield.
- **(f) The single riskiest assumption** in (e).
