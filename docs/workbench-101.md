# Workbench 101

A teaching doc for someone new to this repo. Read it top to bottom; by the end you
should understand why the workbench exists, how one unit of work flows through it,
the one law that holds the family together, and where it is going.

Two status markers appear throughout, and the difference matters:

- `verified` - confirmed against the code on `main` as of 2026-07-21.
- `intent` - designed and written down, but not yet code.

Every claim cites the file that backs it. The paths are part of the teaching: when a
sentence names `cmd/gate/internal/verify/verify.go`, the fastest way to learn is to
open that file. A drift log at the end lists every place the repo's own docs and its
code currently disagree.

---

## 1. Why this exists

The operator of this repo stopped hand-writing code and made agents do essentially
all of it, to find out what breaks. What breaks is trust. The longer an agent runs
unattended, the less you can afford rules that live in prose: a sentence in a
CLAUDE.md is advice the model can skip, misread, or deprioritize under a long
context. A rule that gates something - what may merge, what may spend, what may
touch a credential - is only as strong as its enforcement.

The workbench is the answer: a family of small Go binaries where the safety-critical
decisions live in code the model literally cannot skip - a deterministic risk floor,
an escalate-only model ladder, and a merge gate that refuses to act without a
human-minted grant. The working slogan: **prose shrinks, guarantees grow**. As
models get stronger, how-to prose becomes obsolete and gets deleted; as agents run
longer, every *unenforced* invariant becomes more dangerous and gets promoted into
code.

Three ideas underneath everything else in this doc:

**Composition: small single-job tools, and the model is the orchestrator.** Earlier
generations of this workbench were big orchestrator apps. They were replaced by
small tools that deliberately do not know about each other; composition lives one
layer up, in three kinds of pieces: an **MCP server** is a *capability* (it touches
real state), a **skill** is a *routine* (the only layer allowed to know about
multiple tools), and a **hook** is a *reflex* (it fires whether the model wants it
or not). A corollary is that you accept slop where slop is cheap: work whose output
can be verified mechanically gets routed to a free local model, and its occasional
wrongness costs a retry, not a bad result.

**The shrink-vs-invest test.** When deciding what to keep, the question asked of
every piece is: *was this compensating for a weak model (shrink it), or is it what
makes a strong model safe to trust longer (invest in it)?* Instruction scaffolding
shrinks. Floors, ladders, grants, and audit logs grow.

**Verifiability, never difficulty, is the routing flag.** The formative failure: a
simple-looking classification task where a local model labeled real bugs as false
positives *at confidence 1.0* - confident garbage. The lesson is baked in
everywhere: never trust a model's self-reported confidence; route work down the
ladder only when the output is *verifiable or escalate-safe* (section 5). Signal
ranking, when signals disagree: verifier failures beat model disagreement, which
beats self-reported confidence.

One more observation shapes the whole design: the durable parts keep turning out to
be the **contracts** - the house code style, the verdict schema, the
verifiable-or-escalate-safe rule, the grant. Tools are rungs; rungs get swapped when
the models move.

## 2. The loop - one unit of work, end to end

The single most important mental model. Every tool in this repo exists somewhere on
this path; when you wonder "what does X do," locate X on the loop.

1. **Work is born in dossier** (a separate Rust tool: a markdown task corpus with a
   typed MCP surface): project, then phase, then task. The `/tdd` skill seeds it
   from a design doc; `/work-driver-seed` breaks a described chunk into sized,
   dependency-ordered tasks.
2. **Specs get prepped.** The `/work-driver-prep` skill turns N tasks into per-task
   spec docs plus a *manifest* - a batching of which tasks can safely run in
   parallel, computed from file overlap.
3. **Dispatch.** The `/work-driver` skill drives ship's **driver engine** (a
   separate TypeScript tool), which runs the durable state machine: import,
   dispatch, poll, judgment, land. Each parallel work
   item is a **stream**; each agent identity doing the work is a **seat**. Cloud
   seats are the default. Alternatively `--engine session` makes the Claude session
   itself the engine, recording every state transition through **workbench-mcp**
   (section 8) into the **driver-state ledger** - an append-only, hash-chained
   record that lets a fresh session resume a crashed run. `verified` The session
   engine now runs in *thin-orchestrator* mode: each task's implementation runs in a
   delegated subagent with its own worktree, the parent holds only structured
   summaries and PR URLs, and parent-to-child links are recorded in the ledger - see
   `docs/features/session-orchestrator/spec.md`.
4. **A PR opens per stream**, ending with a `Provenance:` footer naming the seat,
   model, and pipeline that produced it.
5. **The review panel fires** - several AI reviewers comment on the PR. The
   `/review-digest` skill runs a cheap local-model extraction pre-pass over the
   comment pile (extract, don't judge); `/review-coordinator` is the judge over the
   reviewers; its findings drive address cycles that re-dispatch fixes onto the same
   PR branch. (`/pr-risk` is the sibling skill that decides how much review a PR
   needs in the first place, over triage's floor.)
6. **Merge authorization.** `gate gate -repo <r> -pr <n> -grant <grt_...>` checks an
   operator-minted **grant** (a signed, scoped, expiring permission - section 6),
   gathers evidence from GitHub, runs the verifier ladder, reduces all verdicts into
   one, and exits 0 pass / 1 blocked / 2 parked / 3 refused / 4 error. **Parked**
   means "stopped, with the full question packaged for a judge"; escalations park
   for `gate judge -auto`, a frontier model fed *only* recorded artifacts - and the
   grant's ceiling re-applies after judgment.
7. **Merge executes** - the driver's merge tail (or the operator) performs the
   merge. Gate's own `-live` path still records `merge_not_implemented` and prints
   the exact `gh pr merge --match-head-commit` command rather than running it
   (`verified`, section 6).
8. **Record and reflex.** Merge facts are recorded back into ship and the ledger;
   hooks ferry GitHub events into state without relying on agent diligence; the
   dossier task closes.
9. **Observe.** `gate explain` and `gate audit` reconstruct any decision from the
   log alone; `gate next` shows the operator what needs them; **console** renders
   the same picture in a browser; **flare** pushes escalations as notifications. The
   operator's own read-outs are skills over the same artifacts: `/wip` for what is
   in flight right now, `/shipped` for a retrospective on what landed, `/provenance`
   for which pipeline produced each PR.

Keep this loop in mind through section 4: steps 1 and 8 are the State plane, 3 is
Execution, 5-6 are Verification, 6's grant is Capability, 9 is Observability, and
the skills named at each step are the Composition layer - thin policy files the
agent runs, each one the *routine* that knows which tools to call in what order (no
tool knows about another). The durable guarantees live in the binaries below them;
the skills are the swappable rungs.

## 3. The repo and the boundary law

`pers/workbench` is the home for the Go agentic-infra family: **one repo, one Go
module** (`github.com/itsHabib/workbench` - see `go.mod`), deliberately *not*
multi-module. Every boundary that matters is directory-based (each tool's guts live
under `cmd/<tool>/internal/`), and nothing outside the module needs to pin a Go
dependency on it - ship is TypeScript and dossier is Rust, and both read the JSON
*schema files*, not the Go types. The layout stays multi-module-*ready* so a future
split is a `git mv`, not a refactor; the split trigger is an outside-the-module Go
consumer appearing (`docs/DESIGN.md`).

**The one law: share contracts, not call stacks.** Tools compose at runtime through
*artifacts* - typed JSON records on disk plus exit codes. The only in-process
sharing is vocabulary and mechanism, never another tool's decision logic. This is
not a convention: the `hygiene` job in `.github/workflows/ci.yml` fails the build if
it is violated (`verified`), with three checks:

- `contracts/` is a leaf - it imports nothing else in the module.
- `local/` is a mechanism leaf - it imports at most `contracts`.
- No tool under `cmd/` imports another tool's code.

The layout, top level down:

- **`contracts/`** - the shared vocabulary. `verified` contents: `verdict.go` (the
  Verdict type and its decision/class constants), `envelope.go` (the artifact
  envelope and its kinds), `schema.go` (the embedded `verdict-v0.3.0.json` schema),
  plus sub-domains `contracts/driverstate` and `contracts/execution`, each carrying
  types, JSON schemas, and pure validation. The carve-out that keeps this honest:
  *contract-law validation* (pure, stdlib-only, I/O-free "is this a valid
  instance") lives beside the types; anything deciding what to *do* with a valid
  instance stays in the owning tool.
- **`local/`** - a shared *mechanism* package: structured calls to a local Ollama
  model plus the escalate-on-uncertainty gate. `verified` in `local/local.go`:
  `Ask` runs the model, then a gate that escalates first on verifier failure, then
  on low confidence - the verifiable-or-escalate-safe rule as code. Mechanism
  packages are allowed at top level because they carry no tool's decision logic.
- **`driverstate/`** - the second mechanism package, newer than `local/`: the
  write-side mechanism of the driver-state plane - single-writer-per-run leases and
  hash-chained, crash-safe appends (`driverstate/doc.go`). It imports only
  `contracts/driverstate`. (Its doc comment says CI leaf-checks it; CI does not yet
  - see the drift log.)
- **`cmd/<tool>/`** - one binary per **tenant** (a tool living in the module with
  its guts private). `verified` tenant list, twelve today: `console`, `custody`,
  `dispatch`, `driverstate`, `eval`, `flare`, `gate`, `local`, `runway`,
  `tracelens`, `triage`, `workbench-mcp`.

The family assembled by *lazy migration*, not big-bang: tools graduated in when next
touched. flare was the founding tenant (rewiring it to `contracts` deleted the third
hand-rolled verdict parser - the exact debt the repo was stood up to pay), and gate
arrived last (PR #59, 2026-07-17). The convergence went one step further in PR #62:
gate's verify types are now Go *aliases* of the `contracts` types
(`cmd/gate/internal/verify/verify.go`), so there is one verdict vocabulary and gate
keeps only its reducer.

**The house style is itself one of the durable contracts.** Six principles in the
Dave Cheney lineage - no `else` / line-of-sight code, nesting at most two deep,
policy separated from mechanism, composition of single-responsibility layers, small
sharp APIs, errors are values - stamped identically into every repo's CLAUDE.md by
the `/eng-philo` skill and enforced by golangci-lint (`.golangci.yml`: `gocognit`,
`nestif`, `cyclop`, `revive`). The lint
cannot make an agent name things the way you would, but it keeps the bones identical
whether you are watching or not.

## 4. The five planes

The Fable-era redesign decomposed the workbench into five responsibilities, called
**planes**. A plane is a role, not a service - several tools can serve one plane,
and the decomposition earned its keep because it *names the postmortems*: every
recurring failure turns out to be one plane doing another plane's job.

1. **State (remembers)** - the substrate everything writes through. Not a database,
   a *contract*: typed, append-only, content-hashed artifacts with explicit
   provenance (evidence, then verdict, then action), joinable without prose keys.
2. **Execution (does)** - dispatches work, gathers evidence, emits result
   artifacts. Execution never judges its own work.
3. **Verification (judges)** - the keystone; produces the typed verdict via the
   ladder (section 5).
4. **Capability (bounds)** - scoped, timed, capped authority as artifacts: grants.
5. **Observability (explains)** - read-only views computed from State's artifacts
   alone.

Plus **Composition** - the only opinionated layer: the agent plus thin policy files
(skills), gluing planes into workflows.

Who actually implements each plane today:

| Plane | Incumbents today |
|---|---|
| State | gate's hash-chained artifact log; the driver-state ledger (`driverstate/`); dossier (work items); ship's SQLite (runs) |
| Execution | ship's driver engine (cloud, N parallel); the session engine (thin orchestrator); runway (foreground run controller, now with a Rooms microVM backend) |
| Verification | gate's `verify` (reducer + ladder); triage (floor + advisory); tracelens detectors; the `local` mechanism |
| Capability | gate grants (HMAC, scope/TTL/tier/cycles); custody grants for vendor credentials (in flight - section 9); the pretool-guard hook (the harness's tier-3 floor) |
| Observability | gate `explain`/`audit`/`next`; console (read-only web); flare; tracelens reports |
| Composition | the loop skills (`/tdd`, `/work-driver-seed`, `/work-driver-prep`, `/work-driver`, `/review-digest`, `/review-coordinator`, `/pr-risk`, `/wip`, `/shipped`, `/provenance`) plus dispatch (placement policy as code) |

Three amendments sharpen the model. Each one is a response to a real failure, not a
principle for its own sake:

- **Amendment 1 - Verification must have a mandated internal structure, or it is
  just "an LLM said so."** A plane that "produces a verdict" buys nothing if the
  verdict is one model's unchecked opinion. So the structure is fixed for every
  tool, not reinvented per tool: the escalate-only ladder (section 5), where a
  deterministic floor always runs and the model layer may only escalate above it.
  The failure this prevents: before the shared `contracts`, four different tools had
  each hand-rolled their own "is this OK?" parser, so nobody owned what a verdict
  even meant.
- **Amendment 2 - Capability must bound every effectful verb, including writes to
  State.** Gating the obvious action (the merge) is not enough if the bookkeeping
  around it is unguarded. The motivating bug was a function called `markMerged`: it
  wrote "this PR merged" into State without any check that it actually had -
  Execution silently persisting an unverified fact. The fix makes such a write
  *unrepresentable*: recording any outcome now requires both a live grant and a
  supporting verdict, enforced in code rather than by convention.
- **Amendment 3 - Observability is read-only and owns no authoritative state.** A
  view that explains decisions must never *become* the source of one, or you can no
  longer tell what actually happened from what some dashboard happened to cache. One
  deliberate exception, because purity that forces bad engineering is not worth it:
  a notification sink like flare may keep *derived operational* state - a cursor
  marking how far it has read, a dedupe set so it does not notify twice - but never
  an authoritative decision. The log stays the truth; flare only tails it.

The payoff of naming the planes is a diagnostic: **every recurring failure turns out
to be one plane doing another plane's job**, and identifying which plane makes the
fix obvious. `markMerged` was Execution writing State without going through
Verification (Amendment 2). A merge policy written in a skill's prose was
Composition carrying what Capability should enforce - so it moved into a grant. A
diagnostics tool that hardcoded another tool's state paths was Observability
reimplementing State's own resolution - so it learned to read the artifacts instead
of guessing the paths. Once the five planes are shared vocabulary, postmortems get
shorter.

## 5. The ladder

One law seen from two angles.

**The model-routing ladder** decides which model gets which work: a free local 7B
model at the bottom (clerk work), the everyday model in the middle (home base), the
apex model on top (climbed only for genuine judgment). The routing rule for the
bottom rung is three words: **verifiable or escalate-safe**. Either a cheap
deterministic check can verify the output (quoted evidence appears verbatim in the
source; the label is inside an enum), or a wrong local answer only ever adds a cloud
call and can never ship a bad result. Either property sends the work down; neither
keeps it up. And never trust self-reported confidence (section 1's confident-garbage
story is why).

**The verifier ladder** is the same law inside gate, with three rungs by producer
*class*:

1. **Deterministic floor** (class `code`) - always runs, can never be lowered.
2. **Local model** (class `local-model`) - may pass or escalate, **never block**.
   `verified`: `ErrLocalBlock` = `"ladder_violation_local_block"` in
   `cmd/gate/internal/verify/verify.go`. Rationale: small models confabulate on
   dense content; escalation is the safe failure.
3. **Premium judgment** (class `judgment`) - resolves escalations but **cannot
   override a code block**. `verified`: in `Reduce`, the block path returns before
   the judgment carve-out; the comment says a judgment pass must not launder a
   missing floor.

"Cannot override" is again just statement order in one function. `Reduce`
(`cmd/gate/internal/verify/verify.go`) handles the block and the missing-floor cases
and returns *before* it ever consults the judgment - so a judgment can only ever act
on a verdict that was not already blocked:

```go
if out.Decision == DecisionBlock {
    return out, nil          // red stays red - judgment is never reached
}
if !hasCode {
    out.Decision = DecisionEscalate
    return out, nil          // no floor ran - escalate, never silently pass
}
if judged != nil {
    out.Decision = judged.Decision   // judgment only decides here, past both guards
    return out, nil
}
```

The ladder law is encoded as reducer errors plus pinned tests, all `verified` in
`cmd/gate/internal/verify/verify.go`:

- Monotone composition: worst decision wins (block > escalate > pass), max tier
  wins, min confidence carries.
- Fail closed on the unknown, three ways: unknown tiers rank highest; unknown
  producer classes are rejected (`unknown_producer_class`); unknown decisions are
  rejected (`unknown_decision`).
- The grant's tier ceiling re-applies *after* judgment. `verified`: `act()` in
  `cmd/gate/main.go` re-checks `grant.TierWithin(reduced.Tier)` and parks with
  "verdict tier %s exceeds grant ceiling %s"; `judge` routes back through `act()`,
  so a judgment pass cannot launder a high-risk tier past the ceiling.

**What the tiers mean.** A **tier** classifies who must approve a change, and the
classifier is a rubric file, not code intuition: `cmd/triage/RUBRIC.md` *is* the
policy, editing it is itself a T3 change, and every classification records the
rubric's git SHA.

| Tier | Name | Human requirement | Example floor triggers |
|---|---|---|---|
| T0 | auto | none - auto-merge eligible (narrow slice) | pure refactor, tests-only, non-policy docs |
| T1 | standard | one peer review | internal behavior change |
| T2 | sensitive | owner (CODEOWNERS) review | public API, persisted data shape, CI/infra, concurrency |
| T3 | critical | owner + adversarial skeptic + author "why safe" defense | auth/secrets, money, destructive migration, the rubric itself |

Triage computes `floor = max(all deterministic signals that fire)`, then
`final = max(floor, trusted agent escalation)` - the advisory layer may only
*raise*. Its trust boundary: a proposal's quoted evidence must appear **verbatim in
the diff** or it contributes nothing; confidence is recorded, never trusted.
Fail-closed defaults: agent failure keeps the floor but content-sensitive diffs bump
to T2; a missing rubric means T2; an unknown path means T1, never T0. Exit codes: 0
classified, 1 operational failure - an error is never a tier. `verified` down to
the newest hardening: the floor fails closed on empty stdin and oversized diff lines
(`cmd/triage/internal/floor/parse.go`, PR #70).

**One ordering, structurally shared.** Tier ranking lives in one tiny package -
`cmd/gate/internal/tier` - imported by *both* verdict composition and grant
ceilings. Its package comment says it plainly: "so the two can never drift apart."
`verified` (note the location: it is under `cmd/gate/internal/`, not a top-level
`internal/`).

**A known, held wart:** multiple judgments are last-one-wins. `verified` still
true: `Reduce` loops `if isJudgment(v) { judged = &verdicts[i] }`, silently keeping
the last judgment in slice order, which contradicts the reducer's
order-independence claim. The closure TDD holds this deliberately: the fail-closed
fix (reject more than one judgment and escalate) comes after join semantics are
designed. Not yet fixed - and the honesty of tracking it is the point.

**Two load-bearing choices in the verdict schema** (both in `contracts/`):

1. `Decision` (pass/escalate/block - who may proceed) and `Tier` (T0-T3 - who must
   approve) are **orthogonal axes**. Collapsing them fails on the first real PR: a
   floor emits a tier with a pass; a CI readback emits a decision with no tier.
2. `Producer` is a structured `{class, impl}` pair - class carries ladder
   semantics; impl (`qwen2.5:7b`, `claude-cli`, `operator`) is provenance only, and
   the reducer never branches on it. It used to be a `"class/impl"` string; that
   silently broke class matching, so it became structural.

A closing historical note: floor-in-code plus escalate-only was invented
independently twice (triage, then tracelens) before being promoted to plane law.
Convergent evolution is decent evidence a rule is real.

## 6. Gate - the flagship

One Go binary deciding whether a PR may merge. Everything the previous three
sections describe in the abstract is concrete here, which is why it gets the deep
dive.

**Surface.** `verified` verbs in `cmd/gate/main.go`: `grant`, `gate`, `judge`,
`explain`, `next`, `audit`, `backtest`, `stress`. `verified` exit codes:
`codeMerge=0 / codeBlocked=1 / codeParked=2 / codeRefused=3 / codeError=4` - a
load-bearing **seam** (a stable boundary callers branch on). A nice detail showing
the seam is taken seriously: flagsets use `ContinueOnError` because `ExitOnError`
would `os.Exit(2)`, colliding with `codeParked` and making a typo read as a park.
Packages point strictly downward: `state` (append-only hash-chained log) →
`capability` (signed grants) → `evidence` (real `gh` reads) → `verify` (schema,
rungs, reducer, auto-judge) → `observe` (explain/audit/next, read-only).

**The artifact contract.** Every step is a typed `Artifact` (`evidence`, `verdict`,
`grant`, `action`, `escalation`, `judgment`), grouped by run id, linked by a
`Parents` field, hash-chained. Three properties fall out:

- **No outcome exists without its lineage** - every action's parents must name the
  reducer verdict it acted on and a live grant; no gate code path records an outcome
  without both, and the reader rejects any that lack them.
- **Observability needs nothing but state** - `explain` reconstructs the whole
  decision chain from the log alone.
- **A parked artifact carries everything the eventual judge needs** - parking with
  a pointer back into prose is exactly the leak the design exists to prevent.

In code, that first property is not a convention you have to remember - it is the
shape of the one function that writes anything. Every outcome gate can reach
(`blocked`, `parked_for_judgment`, `capability_refused`, `would_merge`) is recorded
through a single closure in `act()` (`cmd/gate/main.go`), and the verdict id and
grant id are its first two arguments - you cannot record an outcome and leave either
out:

```go
record := func(kind, outcome string, extra map[string]any) error {
    body := map[string]any{"outcome": outcome, "verdict": reducedID, "grant": grantID}
    // ...
    // Parents[0] = the reduced verdict; readers join outcome -> Parents[0] -> Subject.
    _, err := e.st.Append(kind, run, []string{reducedID, grantID}, body)
    return err
}
```

Two more code facts make "a *live* grant AND a supporting verdict" literal.
First, the grant is re-checked at effect time, not just at run start - evidence
gathering and verification take real time, so `act()` calls
`capability.Check(...)` again and, if the TTL has lapsed, records
`capability_refused` and nothing else. Second, the read side fails closed on the
same contract: `cycleCount`, `explain`, and `audit` all resolve `Parents[0]` to the
verdict, and error out ("outcome %s parent %s not in log") rather than assume a
default if it is missing. This is the exact opposite of the `markMerged` bug from
section 4, which wrote "merged" into State with nothing in its lineage - here an
outcome cannot be recorded without both parents, and a forged one cannot be read as
valid. (Enforced, to be precise, by `act()` being the sole writer plus readers
failing closed - a runtime contract the hash chain makes tamper-evident, not a
compile-time impossibility.)

**Grants.** A **grant** is an operator-minted, HMAC-signed capability. `verified`
fields in `cmd/gate/internal/capability/capability.go`: `Repo` + `Action` (scope),
`MaxTier` (tier ceiling), `MaxCycles` (review-cycle ceiling; 0 means unbounded, the
honest back-compat reading of grants minted before the field existed), `ExpiresAt`
(TTL). Checked before evidence gathering *and again* before judgment. `verified`
refusal codes, all eight: `grant_expired`, `grant_scope_mismatch`,
`grant_bad_signature`, `grant_tier_exceeded`, `grant_cycle_exceeded`,
`grant_bad_tier`, `grant_bad_cycles`, `grant_key_missing`. Exit 3, nothing recorded
but the refusal. Newest hardening (`verified`, PR #74): minting into a state dir
with no existing log refuses unless `-init` is passed - so a typo'd `-state` path
cannot silently start a parallel ledger.

**The tamper model, stated honestly.** The hash chain is tamper-*evidence*, not
access control. Naive replay catches body edits, broken links, and reordering - but
not tail truncation, whole-log deletion, or a wholesale rewrite-with-rehash. Those
are closed by the **keyed anchor** (`verified`,
`cmd/gate/internal/state/anchor.go`): an HMAC-SHA256 over the chain head *and the
entry count*, written atomically under the same lock as every append, with the key
held **outside** the state dir (gate refuses a key dir nested under state). Distinct
fault verdicts distinguish rewrite, truncation, deletion, and the benign
one-entry crash window (self-healing via rebind on the next append; rebind refuses
an unproven suffix). The trust boundary: the realistic adversary is drift and
accidental corruption, not a funded attacker - key custody outside the state dir is
what makes the anchor meaningful, and this is not non-repudiation. War story: the
naive state layer lost data three runs out of three under a six-process concurrency
stress; the retry-all lock posture exists because Windows returns ACCESS_DENIED
(delete-pending), not EEXIST, on a racing create.

**Judgment.** `gate judge -auto` hands the frontier model only what state holds -
the escalation question, the verifier verdicts, the recorded diff (`verified`,
`cmd/gate/internal/verify/judge.go`, which also wraps the material in
untrusted-data markers and fails closed on an unparseable reply). If a good judgment
would need more than the artifacts carry, that is a contract bug in the artifacts.
Backtesting agreed with real merge history 3 for 3, including reasoning out a
fork-PR trust boundary from the recorded diff alone.

**Review evidence hygiene.** The reviewer-comment consolidation drops stale and
resolved bot comments - a comment thread that is resolved, or anchored to a commit
other than the judged head, no longer counts against the PR, and an *all*-stale
panel escalates as unreviewed rather than passing (`verified`,
`cmd/gate/internal/verify/reviews.go`, PR #75). Absence never reads as green.

**The operator inbox.** `gate next` (`verified`, `cmd/gate/internal/observe/inbox.go`)
projects the log into what needs a human: runs parked for judgment and the grant
ledger, each with paste-ready commands. `GATE_STATE` / `GATE_KEY` environment
defaults mean the operator stops retyping `-state`/`-key` flags. And **console**
(section 7) renders the same inbox in a browser.

**Enforcement, in one honest line** (`cmd/gate/docs/enforcement.md`): *"today the
capability plane is discipline plus an audit trail, not prevention."* Gate becomes
*enforcing* only when a repo's branch protection requires the gate status check. The
machinery for that now exists in this repo (`verified`,
`.github/workflows/gate.yml`) and is worth reading as a security artifact in its own
right: it triggers on `workflow_run` so it runs in the trusted base-repo context
even for fork PRs; it builds gate from the *base* checkout so a PR cannot edit
gate's own code to neuter the check that governs it; it mints an ephemeral grant
into a throwaway temp dir so no signing secret ever touches CI; and it binds the
posted status to the exact judged SHA so a force-push cannot get a green stamped
onto a different commit. It ships **dormant** - armed only by the repo variable
`GATE_ENFORCE=true`, and still off because gate's model rungs need a runtime a
stock GitHub runner does not have. Until it is armed and branch protection requires
the `gate` context, enforcement remains discipline plus an audit trail. `intent`
beyond arming: full enforcement means the required check *plus* no bypass credential
for governed agents (section 9).

**Live merge: not implemented.** `verified`: `act()`'s live path records
`merge_not_implemented`; only the exact `gh pr merge --match-head-commit` command
string is composed. Say "dry-run plus printed command," not "it merges."

**See it.** `gate explain -run <id> -html` writes a self-contained decision-trail
page - no server, no network. The embedded demo fixture
(`cmd/gate/docs/demo/README.md`) is exactly the earmarked story: judgment passes,
but the grant ceiling (max tier T1) refuses the reduced T2 verdict and parks the run
- layered authority, visible. Operational state and keys live at `~/pers/gate`,
outside the repo.

## 7. Tenant tour

Twelve tenants, each one binary, each private below `cmd/<tool>/`. The repeated
shape to watch for: **policy over mechanism, joined by artifacts** - one side
decides, the other executes, and they meet at a JSON seam, never an import.

- **gate** - section 6.
- **triage** - PR risk: the deterministic floor plus the escalate-only advisory,
  two binaries (`triage-floor`, `triage-advisory`) whose *names are load-bearing* -
  gate shells `triage-floor` by name as an external process
  (`cmd/gate/internal/verify/floor.go`), composition by seam, never import. Policy
  lives in `cmd/triage/RUBRIC.md` (section 5's tier table); `cmd/triage/labels/` is
  the held-out oracle corpus.
- **console** - `verified`, the newest observability tenant: a read-only local web
  surface over gate's inbox (`cmd/console/docs/DESIGN.md`). It is a pure renderer
  over gate's own JSON - it shells the gate binary (`gate next -json`,
  `gate explain -json`) and never imports `cmd/gate` or reads the log directly. GET
  only; judging and minting stay in the CLI. Hardened the way a localhost tool
  should be: loopback-only bind, Host-header pinning against DNS rebinding, strict
  CSP, `nosniff` on every response.
- **custody** - the Capability plane extending from merges to credentials: a
  localhost credential broker so agents hold scoped, expiring, minted capabilities
  instead of the operator's raw API keys. In flight - section 9 tells the story;
  what is merged today (`verified`) is the HMAC grant envelope and the verb-registry
  CLI (`cmd/custody/main.go`, `cmd/custody/internal/grant/`), with `keys` and
  `serve` registered as explicit not-yet-implemented placeholders.
- **dispatch** - the placement-decision plane: a versioned, content-hashed policy
  file plus a task descriptor in, a deterministic placement (engine, provider,
  model, effort, runtime) plus an append-only receipt out
  (`cmd/dispatch/docs/DESIGN.md`). It *decides*; ship's dispatch verb *executes*.
  Verbs `decide` and `validate`; errors are single-line JSON `{code, message}`; no
  placement is ever emitted on a non-zero exit.
- **runway** - foreground execution-runtime controller: one admitted request
  becomes one run dir, one ordered lifecycle journal, at most one terminal
  `result.json` (`cmd/runway/README.md`). Verbs run/watch/logs/cancel/result/
  reconcile; state at `~/.runway`. `verified` newest capability: a Rooms placement
  backend (`cmd/runway/internal/backend/rooms/`) that places a work bundle into a
  microVM guest by shelling the Rooms CLI - and the first real WorkSpec has been
  placed through it end to end
  (`docs/features/runway/EVIDENCE-first-rooms-placement.md`).
- **driverstate** (the CLI tenant) - the human/cron twin of the MCP surface over
  the ledger mechanism: `record` claims the run lease, appends, and releases in one
  shot. `verified` verbs: `record`, `state`, `render` (a human-readable run view),
  `runs`, `rollup` (join a parent run to its child sub-runs), `verify`.
- **workbench-mcp** - the unified MCP surface: JSON-RPC over stdio. `verified`
  verbs in `cmd/workbench-mcp/internal/server/verbs.go`, six today:
  `driver_record`, `driver_state`, `driver_runs`, `driver_verify`, plus
  `driver_transition` (an ergonomic recorder: flat facts in, a deterministic
  content-addressed event minted server-side, retry-idempotent by construction) and
  `driver_rollup`. The session-lifetime lease auto-renews at half the lease TTL,
  and there is deliberately no `driver_renew` verb - a test asserts its absence
  (`cmd/workbench-mcp/internal/server/server_test.go`).
- **flare** - the escalation-routing sink: tails gate's log and ship's receipts,
  raises a toast/webhook/Slack message on blocks, and gate escalations carry a PR
  click-target. It is the Amendment 3 tenant: it keeps cursors and dedupe state at
  `~/.flare/`, never decisions - best-effort push over authoritative pull
  (`cmd/flare/README.md`).
- **tracelens** - agent trace diagnostics, consumed via its CLI exit-code seam
  (0 pass/escalate, 1 block, 2 error), never imported.
- **local / eval** - the clerk-work CLI over the `local/` mechanism, and the
  local-exportability oracle: `eval` runs a labeled dataset through the same
  primitive and scores whether a task class is safe to route local
  (`cmd/eval/main.go`), including a verbatim-quote confabulation check.

## 8. Auto-mode doctrine - the six defaults

`docs/auto-mode-defaults.md` (`verified`, merged) distills what gate and triage got
right into portable doctrine for *any* auto-decider - explicitly working defaults,
not law: each names the failure mode it guards, and the bar is "works well," not
"conforms."

The generalization: gate and triage are not two tools that happen to rhyme - they
are two rulebooks implementing **one contract**:

```
(action, observables, rulebook, grant) -> {pass | park | block} + rule-fired + artifact
```

Gate implements it for merges, triage for PR risk, and the Claude Code harness
config (settings plus hooks) for tool calls. Custody (section 9) is the fourth
instantiation, for vendor-API reach. They converge on shared vocabulary in
`contracts/`, never shared call stacks. The thesis restated: autonomy is safe in
proportion to how much of the decision surface is deterministic code the model
cannot skip - the untapped potential of auto mode is not a smarter judge, it is **a
rulebook that compounds**.

**The tier model** sorts actions by *capability*, not by how scary the command
looks (reversible? observable after the fact? bounded blast radius?):

| Tier | Capability | Mechanism |
|---|---|---|
| 1 - free | read-only / trivially reversible | allow rules; should never prompt (every prompt here is friction tax) |
| 2 - consequential, auditable | durable state you can see and undo (open a PR, dispatch a run) | allow rules + observability hooks |
| 3 - irreversible or authority-bearing | merge, force-push, delete, publish, spend, mint | deny rules + pre-execution guard hooks + external gates (grants) |

The key line: an allowlist entry is safe in proportion to the gates *behind* it -
`gh pr create` is tier 2 *because* merge is gated; without gate it would be tier 3.

**The six defaults**, each guarding a named failure mode:

1. **Deterministic floor, advisory ceiling** - the model layer may only escalate,
   never downgrade or approve. This converts model accuracy from a safety question
   into a *cost* question, which is exactly what makes a free 7B viable as the
   advisory layer.
2. **Classify observables, not intent** - floor rules key on script-computable
   facts (globs, LOC, CI state, grant presence, command text), never "does this
   look risky." Litmus test: can the rule be a unit test with a fixture? If
   deciding requires *understanding*, it belongs to the advisory layer or a human.
3. **Fail closed, and make closed cheap** - unknown input parks, and every
   park/block/refusal prints its own remedy. A fail-closed path with an expensive
   escape hatch trains operators toward bypass, which costs more safety than the
   rule bought.
4. **Every decision is an artifact** - inputs, rulebook version, **rule-fired**,
   verdict; hash-chained where the decision carries authority. Determinism you
   cannot replay offline is not determinism. Rule-fired is the field people skip,
   and it is what turns the log into tuning evidence for default 6.
5. **Authority is minted, not inferred** - the classifier decides *tier*, a grant
   decides *ceiling*, always separate axes. An accumulation of correct verdicts
   never widens what the classifier may do; only a fresh human-minted grant does.
   This bounds the classifier's own failure modes (bugs, prompt injection of the
   advisory layer) at the ceiling.
6. **Tune by moving the boundary, not softening it** - when auto-mode annoys,
   promote a new deterministic rule from audit-log evidence instead of a model
   override (an override quietly makes the model the authority, unwinding defaults
   1 and 5). Every promoted rule is a category handled exactly, via a reviewed PR -
   the deterministic layer compounds while a probabilistic judge stays at its error
   rate.

**The rulebooks in operation today:** portfolio actions (gate + triage, state at
`~/pers/gate`, hash-chained log) and harness tool calls - three settings layers with
distinct jobs: global personal defaults (the tier-1 read-only floor, the tier-3 deny
list, the guard hook), the checked-in project rulebook (reviewed by PR, so *the
rulebook governs itself*), and a local scratch file drained on a cadence. The named
fail-open hole: wildcards accreted in local scratch silently undoing per-verb
curation - dual-shell platforms need every rule in both shells plus a deny backstop.

The pretool guard (`~/pers/hooks/scripts/pretool-guard.sh`, `verified` - it lives in
the hooks repo, not this one) is the harness's tier-3 floor: it regex-refuses
command shapes with no sanctioned use (force push, repo delete, visibility flips,
credential and gate-state touches - and already custody's mint/keys verbs inside
governed sessions), each refusal with a remedy line, in every permission mode. On
merges it is deliberately *shape*-checking, not policy-checking: a bare
`gh pr merge` is refused as bypassing gate, while the gate-emitted form carrying
`--match-head-commit` passes - merge *policy* stays in gate, because duplicating it
in the guard would create a second policy source.

**Deliberately not now** (boundaries are part of the doctrine): model-in-the-loop
approvals in the floor (the floor's whole value is that a model failure cannot cause
an action); a generic configurable rules engine for hypothetical users (rulebooks
encode *this* workflow); a separate grant system per tool (grow the one grant
substrate, scoped by domain - which is exactly the custody story next).

## 9. Where it's going

Three threads, all honestly marked. Each one removes a reason a run still needs a
human in the loop, so the direction they share is a fully autonomous cloud run - the
loop in section 2 executing end to end with the operator reading the audit trail
after, not gating it during. The pattern underneath: the center of gravity keeps
moving toward versioned artifacts and engine guarantees, with harness-specific
skills as thin projections.

### Custody: grants for credentials, not just merges

Today agents read raw API credentials from a plaintext file. The secret enters the
model transcript - from which it can leak anywhere a transcript goes - and it
carries the operator's *full identity*: every scope the human has, forever,
unaudited. Custody (`docs/features/custody/spec.md`) converts "the agent holds my
identity" into "the agent holds a capability I minted": a localhost reverse proxy
where the agent calls `http://127.0.0.1:8127/<key>/<vendor-path>` with a minted
grant header, and custody canonicalizes the target, validates the grant (HMAC, TTL,
key scope), matches the request against granted action rules, injects the real
secret from the OS credential store, forwards *without following redirects*, and
appends a replay-sufficient JSONL line. Refusals fail closed and print the exact
`custody grant` command that unsticks the work.

The seam discipline holds: custody decides *reach* (which requests a grant may
make). It does not decide placement (dispatch), merge (gate), or risk tier
(triage). It shares their contract shape - section 8's one-liner - not their call
stacks. Two design calls worth knowing: the grant *mechanism* is deliberately copied
from gate and versioned from day one (convergence into `contracts` is a later,
mechanical PR once two real consumers have shaped the seam), and the whole
authorization rests on one invariant - *the exact semantic target that was matched
is the target that is sent* - which is why the spec spends most of its ink on
canonicalization, redirect refusal, and deny-by-default query handling, and why the
threat-model section states plainly what a single-box broker does and does not buy.

Status, precisely: the spec is merged; the grant envelope and CLI dispatcher are
merged (`verified`, `cmd/custody/`); the manifest loader and credential store are
in review; the proxy engine (`custody serve`) - the security-critical core - is
queued. `intent` until the phase-1 validation gate passes: one real high-stakes key
wired through custody for a week of normal agent use, zero raw-secret occurrences in
transcripts, the plaintext entry deleted, and `custody log` answering "what did
agents touch this week" in one command.

### Enforcement moves to GitHub's boundary

Gate's verdicts become *prevention* when GitHub refuses the merge without them. The
workflow already on `main` (section 6's `gate.yml`) is the first half: a required
`gate` status check, computed fork-safely from base-built code, dormant until
armed. The second half is the closure TDD's D7: real capability enforcement lives at
*GitHub's* boundary - the required check plus credential custody, meaning governed
agents simply hold no credential that can bypass the check. Local HMACs are not the
security claim; the branch-protection rule is. `intent` beyond the canary repo.

### Cross-harness closure

`docs/features/agentic-workbench-closure/spec.md` is the program-level hypothesis:
the workbench becomes trustworthy *across model generations* when the durable center
is versioned artifacts plus engine guarantees, and skills are installed projections
over one canonical workflow - proven when Claude *and* Codex each drive the same
real review-fix-to-land loop. The program gate (**Gate B**): a fresh Codex seat and
a fresh Claude seat each close one real actionable-review loop through the same ship
artifact boundary, with zero mechanism-repair interventions - and the intervention
taxonomy is itself typed, so an unclassifiable event counts as mechanism repair;
absence never reads as clean. The key artifact at the review-address seam,
`ReviewFindingsV1`, is still `intent` - it appears in no code anywhere
(`verified` by search). Meanwhile the parts that have landed are real: the
driver-state validation gate passed 2026-07-17
(`docs/features/driver-state/spec.md` §11), the thin-orchestrator session engine
landed with parent/child rollups, and the invariant-dense core (hash chain,
canonical encoding, reducer, import dedupe) gained property-based and fuzz coverage
asserting the laws over the whole input space rather than hand-picked examples -
including the property that distinct import keys mint distinct runs, the exact
class of a real bug.

Still open, and worth saying plainly: live merge, the multiple-judgment reject, and
`ReviewFindingsV1` are intentions, not results.

## 10. Self-test

Answers in parentheses; every one is derivable from the sections above.

1. Why single-module, and what's the trigger for splitting `contracts` out?
   *(directory boundaries suffice; trigger = an outside-the-module Go consumer;
   ship/dossier reading the JSON schema files explicitly doesn't count.)*
2. Why may `local/` and `driverstate/` exist top-level when tools can't share code?
   *(mechanism packages: they carry no tool's decision logic and import at most
   contracts; the boundary law forbids sharing decisions, not mechanisms.)*
3. Why can a local-model verifier never block? *(small models confabulate on dense
   content; escalation is the safe failure; `ErrLocalBlock`.)*
4. Can premium judgment override the deterministic floor? *(No - red stays red;
   and the grant ceiling re-applies after judgment.)*
5. What does the keyed anchor catch that pure chain replay doesn't? *(truncation,
   deletion, wholesale rewrite-with-rehash - because it pins head AND count under a
   key held outside the state dir.)*
6. Why are Decision and Tier separate axes? *(who may proceed vs who must approve;
   a floor emits tier-with-pass, CI emits decision-with-no-tier.)*
7. What made `markMerged` the motivating bug? *(a State write that dodged
   Verification - hence Amendment 2.)*
8. Has gate performed a live merge? *(No - `merge_not_implemented` dry-run
   printing the exact `--match-head-commit` command; enforcement today is
   "discipline plus an audit trail," with the GitHub check built but dormant.)*
9. What's the known reducer wart? *(last-judgment-wins on multiple judgments; held
   deliberately, fail-closed reject is the planned fix.)*
10. Workbench vs platform? *(independent binaries composing through artifacts and
    exit codes; deliberately not a new runtime platform.)*
11. What's the one contract all auto-deciders share? *((action, observables,
    rulebook, grant) → pass|park|block + rule-fired + artifact - gate for merges,
    triage for risk, harness config for tool calls, custody for vendor reach.)*
12. Why is `gh pr create` tier 2 and not tier 3? *(an allowlist entry is safe in
    proportion to the gates behind it - merge is gated by gate; without gate it'd
    be tier 3.)*
13. When auto-mode annoys, what's the right fix? *(promote a deterministic rule
    from audit-log evidence, never a model override - overrides make the model the
    authority and unwind the floor and the grant separation.)*
14. What do T2 and T3 require, and what's special about editing RUBRIC.md? *(T2 =
    owner review; T3 = owner + adversarial skeptic + author defense; the rubric is
    control plane, so editing it floors at T3 and every classification records the
    rubric's git SHA.)*
15. Name the four dimensions of a gate grant. *(scope - repo+action; tier ceiling;
    cycle ceiling; TTL - all HMAC-signed, minted only by the operator.)*
16. In one sentence, what keeps verdict tiers and grant ceilings from drifting
    apart? *(both import the same tiny `cmd/gate/internal/tier` package - one
    ranking, two consumers.)*
17. What does custody decide, and what does it explicitly not decide? *(reach -
    which requests a grant may make; not placement, not merge, not risk tier; it
    shares the contract shape, never the call stacks.)*
18. Why does `gate.yml` build gate from the base checkout instead of the PR head?
    *(so a PR cannot edit gate's own code to neuter the check that governs it -
    the check must be trusted-context code judging untrusted changes.)*

## 11. Glossary

A backstop, not a prerequisite - every term here is also defined at first use above.

- **plane** - one of the five redesign responsibilities (State / Execution /
  Verification / Capability / Observability); a *role*, not a service.
- **tenant** - a tool living at `cmd/<tool>/` in the workbench module, guts
  private.
- **artifact** - a typed JSON record on disk (evidence, verdict, grant, action,
  escalation, judgment...); the only cross-tool channel besides exit codes.
- **seam** - a stable integration boundary (exit codes, binary names, JSON shapes)
  that callers branch on; "load-bearing seam" = breaking it breaks callers.
- **ladder / rung** - the mandated verifier structure: code floor → local model →
  premium judgment (also the model-routing ladder: local → everyday → apex).
- **floor** - the deterministic rung; always runs, can never be lowered.
- **advisory** - a model layer that may only escalate above the floor, never lower
  or approve.
- **verdict** - the shared judgment artifact: subject + decision
  (pass/escalate/block) + tier + producer {class, impl} + findings + confidence.
- **reducer / Reduce** - gate's monotone composition of many verdicts into one
  (worst decision, max tier, min confidence).
- **tier (T0-T3)** - who must approve: auto / one peer / owner /
  owner+skeptic+defense.
- **grant** - operator-minted, HMAC-signed capability. Gate's: repo+action scope,
  tier ceiling, cycle ceiling, expiry. Custody's: key scope, action set, TTL,
  versioned from day one.
- **park** - fail-closed outcome that stops and hands to judgment/human with the
  full question attached (exit 2), vs **block** (red evidence, exit 1) and
  **refuse** (no authority, exit 3).
- **seat** - one agent identity driving work (a Claude session, a Codex task, a
  cloud runner).
- **stream** - one parallel work item within a driver run.
- **merge tail** - the end of a driver run: reviews → gate → merge → record.
- **session engine / thin orchestrator** - the mode where the Claude session
  itself is the engine, delegating each task to an isolated-worktree subagent and
  holding only summaries; resumable from the ledger.
- **rollup** - the read that joins a parent run to its child sub-runs via recorded
  ledger links.
- **key / manifest (custody)** - a named credential behind the proxy, and the
  operator-owned file mapping keys to upstreams, injection templates, and action
  rules (state-dir content, never repo content).
- **canary** - the one repo where branch protection already requires gate's status
  check.
- **dogfood** - proving a tool by running the portfolio's real work through it.
- **MCP / skill / hook** - capability / routine / reflex (section 1).
- **fail closed** - unknown or absent input becomes park/refuse, never a silent
  pass; "absence never reads as green."

## 12. Drift log - where docs and code disagree (verified 2026-07-21)

The discipline: this repo prefers an honest list of disagreements over docs that
quietly overclaim. Two directions of drift exist - docs behind code (stale) and
docs ahead of code (intent not yet delivered). Both are listed.

| Claim | Reality |
|---|---|
| `cmd/gate/docs/DESIGN.md` verb list and refusal codes | Behind code: omits the `next` verb; lists four refusal codes where the code has eight; still calls the cycle ceiling future ("as the integration matures") though `MaxCycles`/`CyclesWithin` are live |
| `cmd/workbench-mcp/main.go` package comment: "the four driver-state verbs" | Behind code: the server registers six (`driver_transition`, `driver_rollup` added) |
| `cmd/driverstate/CLAUDE.md` verb list | Behind code: omits `render` and `rollup` |
| `driverstate/doc.go`: "leaf-checked by CI's hygiene job" | Ahead of CI: the hygiene job does not yet leaf-check the top-level `driverstate/` package (it is compliant in fact - imports only `contracts/driverstate` - but unenforced) |
| `docs/DESIGN.md`: "Today the repo holds `contracts`, `local`, and `flare`; the rest migrate in lazily" | Behind code: migration is long since complete; twelve tenants live under `cmd/` |
| `README.md` layout section | Behind code: omits `dispatch`, `driverstate`, `runway`, `workbench-mcp`, `console`, `custody` |
| Repo `CLAUDE.md` map | Behind code: omits `dispatch`, `runway`, `workbench-mcp`, and the top-level `driverstate/` |
| Live merge | Still `merge_not_implemented` dry-run; the `already_merged` short-circuit is design-only |
| Multiple judgments in `Reduce` | Still last-one-wins (held deliberately in the closure TDD; fail-closed reject is the planned fix) |
| `ReviewFindingsV1` | In no code anywhere - pure intent (closure Phase 1) |
| `custody keys` / `custody serve` | Registered CLI placeholders returning "not yet implemented"; manifest+credstore in review, proxy engine queued |
| `.github/workflows/gate.yml` | Built and merged but dormant: posts nothing until `GATE_ENFORCE=true` and a model-capable runner exist |
| "one repo, one Go module" | One caveat: a nested test-fixture `go.mod` exists at `cmd/gate/docs/features/ci-classify/eval/build/` (an eval fixture, not a real second module) |

Confirmed-in-code anchors, for contrast (all re-checked 2026-07-21): the exit
codes, verbs, ladder law, monotone reduce, and fail-closed unknowns; the eight
grant refusal codes and the `-init` mint guard; the keyed anchor pinning head and
count; the post-judgment ceiling re-check; the triage-floor exec seam; the hygiene
job's three boundary checks; the contracts alias convergence; the stale-bot-comment
filtering; and the six MCP verbs with no `driver_renew`.
