# workbench

The home for the Go agentic-infra family — one repo, one Go module. Tools live
side by side and **share contracts, not call stacks**: they compose at runtime
through artifacts (exit codes + JSONL on disk), and share only *types and
schemas* in-process — never one another's decision code.

Read `docs/DESIGN.md` first — it is the charter: the single-module decision and
why, what's in and out, the boundary law, the lazy-migration policy, and the
triggers that would later split `contracts` into its own module.

New to the system as a whole (not just the module boundary)? `docs/workbench-101.md`
is the teaching doc: the full methodology top to bottom (why it exists, the loop,
the five planes, gate as the flagship, where it is going), opening with a one-screen
Orientation block you can point an agent at to ground it fast.

## Map

- `contracts/` — the shared vocabulary: the verdict schema + Go types every
  verifier emits, and the artifact envelope every producer writes. A **leaf**
  package that imports nothing else in the module and carries no decision logic.
  This is the debt payment — one source of truth instead of a parser per tool.
- `local/` — the shared local-model mechanism: structured Ollama calls + an
  escalate-on-uncertainty gate. A top-level *mechanism* package — carries no
  tool's decision logic, leaf-checked like `contracts` (may import at most
  `contracts`). See `local/README.md` for the eval verdicts and the
  when-to-route-local rule. Its CLIs live at `cmd/local` and `cmd/eval`.
- `cmd/<tool>/` — one binary per tool; its guts stay private under
  `cmd/<tool>/internal/`. Each tool keeps byte-identical scoped guidance in
  `cmd/<tool>/CLAUDE.md` and `cmd/<tool>/AGENTS.md`, plus `docs/DESIGN.md`.
  CI requires the guide pair to stay synchronized so either harness discovers
  the same exit codes, invariants, and checks.
  Twenty today, grouped by what they own. Running agents: `fleet` (the substrate:
  hook-derived identity and liveness, seats, rows, receipts, role-addressed mail, the
  watcher), `org` and `org-mcp` (editable role cards and an optional parent directory), `standup` (the agenda, the record the lead proposes, the operator's confirm, and the apply that compiles cards into fleet rows and mail), `runway`, `dispatch`, `driverstate`, `codexguard`. Deciding what may merge: `gate`
  (the merge-authorization boundary; exit codes 0 pass / 1 blocked / 2 parked / 3 refused /
  4 error are a load-bearing seam), `triage` (`triage-floor`, `triage-advisory`), `review`,
  `reviewfindings`, `escalate` (the agent→human→agent back-channel for parked runs). Seeing
  and being told: `console`, `flare`, `tracelens`, `workbench-mcp`. Local models and secrets:
  `local`, `eval`, `custody`. The cross-tool decision on what `org` owns versus `fleet` is
  `docs/features/org-fleet-boundary/spec.md`.
- `docs/DESIGN.md` — the repo charter. `FOLLOWUPS.md` — the lazy-migration queue
  and deferred decisions (the engineering debt this codebase owes).
  `friction-log.md` — where this repo's tooling and docs failed an agent working
  in it; `/health` reads it for the cross-repo rollup, so tooling friction goes
  there, not in FOLLOWUPS.

## Fleet headless runtime

Use the Go `fleet watch` for persistent polling, recurring lead wakeups, delivery and launches.
The Bash/Python runtime poller is retired. See `cmd/fleet/docs/headless.md` and
`cmd/fleet/docs/run-a-fleet.md`. Desktop loops and native messaging remain a valid desktop
workflow. New Fleet work does not need duplicate Org bootstrap or a per-tick action/message
quota. Org registers editable prose cards; old lifecycle callers are removed during clean cutover. Use `fleet watch status [--json]` for worker evidence between ticks.

## The one rule

A tool may share **types and schemas** through `contracts`. A tool may **not**
import another tool's decision logic. When a tool needs another tool's *output*,
it reads an artifact. CI's `hygiene` job enforces this — it is not a convention.

## Where work happens

**`~/dev/workbench` stays on `main`, clean.** Every change happens in a
worktree — `/worktree-add`, or `git worktree add` under `.claude/worktrees/`.

This is not tidiness. A session that works directly in the main checkout and
leaves it parked on a feature branch produces a tree that *looks* like live
work to everyone who opens the repo afterwards, and there is no way to tell
that apart from real in-flight work without reconstructing history. It happened
between 2026-08-06 and 2026-08-16: the checkout sat on a merged branch, 18,496
lines behind `main`, with a dirty tree whose entire tracked content had already
landed. Two later sessions read it as unclaimed work in progress, and
disproving that cost more than the original cleanup would have.

So, at the start of any session that touches this repo:

```
git -C ~/dev/workbench status --short   # expect no output
git -C ~/dev/workbench branch --show-current   # expect: main
```

Anything else is stale until proven otherwise — check it before trusting the
tree, and never assume a dirty main checkout belongs to a live session.

<!-- BEGIN eng-philo (managed by /eng-philo - re-run to refresh; hand-edits inside this block will be overwritten) -->
## Engineering principles

Dave Cheney lineage: simplicity, clarity, and line-of-sight. Apply these on every change.

1. **No `else`; preserve line-of-sight.** Handle errors and edge cases with early returns and guard clauses. Keep the happy path on the left margin.
2. **Keep nesting at two levels or fewer per scope.** A loop plus a condition is the ceiling. Extract a focused function instead of adding another level.
3. **Separate policy from mechanism.** Validation, state transitions, and business rules sit above persistence, transport, and I/O. Mechanism stays dumb and swappable.
4. **Compose single-responsibility layers.** Each layer or package owns one job, dependencies flow one way, and replacing one piece does not ripple through the rest.
5. **Keep APIs small and sharp.** Export the least callers need, use intention-revealing names, accept narrow inputs, and return concrete values.
6. **Treat errors as values; prefer simple code.** Handle or propagate errors explicitly. Readable beats clever, and a little duplication beats a premature abstraction.
7. **Build only what the outcome requires.** Start from the intended outcome. For each option, abstraction, dependency, or workflow step, ask what requires it, what concretely breaks without it, and whether a simpler alternative is adequate. Prefer existing capabilities and direct implementations; derive information already known. Consider setup, operation, and maintenance, not just line count. Preserve required behavior, correctness, security, accessibility, and authority boundaries. "Nothing worth cutting" is a valid result.

### Go idioms and enforcement

Accept small consumer-owned interfaces and return structs; keep errors lowercase and wrap with `%w`; use guard clauses and preserve line-of-sight.

Enforce with `gofmt`, `go vet`, and golangci-lint `gocognit`, `nestif`, `cyclop`, and `revive`.
<!-- END eng-philo -->

## Checks

```
gofmt -l . && go vet ./...
golangci-lint run ./...
go test ./...
```

CI (`.github/workflows/ci.yml`) additionally runs `go test -race` and the
`hygiene` boundary-law assertions. Third-party Go dependencies are allowed.

<!-- local-offload:start -->
## Local-first offload

Before spending cloud tokens on a mechanical sub-step, check for a free local path (needs the `local` CLI / Ollama on this machine):

- Narrowing a big file list, extracting structure from noisy tool output, shallow classification -> `/offload`
- "Have we solved/decided this before?" questions about the operator's own work -> `/ask-portfolio`
- Triaging a PR's bot-comment pile -> `/review-digest <PR#>`

Deep judgment (code review, risk calls, dense-diff reasoning) stays with the primary model. If `local` is not on PATH, skip silently -- never block on this.
<!-- local-offload:end -->

<!-- BEGIN dev-workbench (managed by /dev-workbench skill — re-run to refresh; hand-edits inside this block will be overwritten) -->
## Dev workbench

These MCPs, planes, and skills are available in any agent session on this machine; the harness injects each tool's signature, so this is the *map* — how they compose — not the per-verb manual. **This is the workbench — gate, flare, console, and escalate live here as `cmd/<tool>`** — so the plane contracts are the most directly relevant. When the signal matches, call the verb; don't ask permission. Stuck on a *knowledge* question about another portfolio repo → `/consult` its steward; only *authority* questions (direction, spend, irreversible calls) go to the operator.

**MCPs (in-session):**
- **dossier** — durable project memory: projects → phases → tasks → artifacts (markdown-on-disk).
- **ship** — the driver engine: dispatch a task to a cloud/local agent and persist the run (dispatch→poll→judgment→land→record); inspect/cancel/replay.
- **channel** — *optional* agent message bus (append-only JSONL, `channel.post/read/list`); post/read to coordinate with peer agents or leave word for the operator; off the normal PR path.
- **playwright** — browser automation when a task needs a real DOM.

**Planes (workbench tenants — CLIs composed via exit codes + JSONL, not MCPs; `itsHabib/workbench` `cmd/<tool>`):**
- **gate** — the flagship: authorization. Evaluates the *exact* PR head against an operator-minted grant + the escalate-only verifier ladder; hash-chained audit log; exit 0 pass / 1 blocked / 2 parked / 3 refused / 4 error. Findings ≠ authorization; gate is the merge boundary. State + keys stay `~/pers/gate`.
- **flare** — notification: best-effort escalation sink over authoritative receipts → its own Slack app/channel. Pure sink; never gates; not built on huddle.
- **console** — read-only local web view of gate's inbox (parked runs + grant ledger); shells the gate binary, owns no authoritative state.
- **escalate** — the agent→human→agent back-channel: ingests the human's decision for a parked escalation and drives `gate resolve`.

**Skills:**
- **/work-driver** [+ **/work-driver-prep**] — drive agent-led impl end-to-end; prep builds the specs + conflict-batched plan.
- **/pr-risk** — size how much review a PR needs (deterministic floor + agent advisory); upstream of the reviewers — it decides *how much*, they *do* it.
- **/review-coordinator** [+ **/review-digest**] — consolidate the AI PR reviewers into one verdict (the judge over the finders); digest pre-triages the bot pile locally.
- **/shipped** · **/status** · **/wip** — retrospective recap · in-flight update · cross-store live board.
- **/consult** — summon a sibling repo's steward for a same-turn answer; knowledge → peer, authority → operator.
- **/worktree-*** — add · list · remove · transfer · where, over `git worktree`.

### The loop

```
dossier task → /worktree-add → spec → ship driver (cloud-first: dispatch→poll→judgment→land→record)
   → PR + CI → /pr-risk tiers it → reviewers fire → /review-coordinator → one verdict
   → gate evaluates the exact head → 0: governed-path authorization → merge
   → authoritative receipts → dossier close-out → /worktree-remove
        ↘ 2: gate PARKS → console / gate next surface it → human decides → escalate → gate resolve → re-judge
        ↘ any attention/terminal receipt → best-effort flare sweep → Slack   (independent; never gates)
```

`/work-driver` coordinates dispatch→poll→land and runs its own review triage inline. `/pr-risk` and `/review-coordinator` are steps you *invoke* — the driver→pr-risk / driver→coordinator wiring is planned, not built, so nothing here auto-delegates.

### Why this shape

Each layer owns one responsibility and is swappable without rippling: dossier owns *what needs doing*; worktree skills own *where work happens*; ship owns *drive an agent + persist the run*; pr-risk owns *how much review*; review-coordinator owns *consolidate the finders* (the bots are swappable under it); **gate owns *authorization* — is this exact head allowed to merge — which is not the reviewers' findings**; **escalate owns *resolution* — closing the agent→human→agent loop a park opens, without ever deciding for the human**; **console owns the *read-only view* of gate's inbox — it explains, never decides**; **flare owns *notification* — a best-effort sink on authoritative receipts, its own Slack app, never blocking the driver, never depending on huddle**; consult owns the stuck path; channel owns optional agent-to-agent messaging (superseding huddle); playwright owns browser. The workbench is a menu, not a checklist — skip what a flow doesn't need.

### The shape underneath

These tools instantiate the redesign's five contract planes — coupled only by typed artifacts (`evidence → verdict → action`), never call stacks:

- **State** (remembers) — dossier + gate's hash-chained log + run/verdict/grant/receipt artifacts; the append-only substrate.
- **Execution** (does) — ship's driver; emits evidence, never judges itself.
- **Verification** (judges) — the escalate-only ladder (deterministic floor → local → premium), monotone `worst`/`max`: gate's reducer, review-coordinator, triage/tracelens.
- **Capability** (bounds) — scoped/timed grants; every effectful verb needs a live grant + a supporting verdict.
- **Observability** (explains) — read-only, storeless views from State: flare, console, /wip, /shipped, /status.

This section is the sixth — **Composition**: the agent + thin policy choosing which planes a task needs. gate is the flagship — the one tool spanning Verification + Capability, holding the merge boundary. The boundaries above *are* the plane laws, not conventions.
<!-- END dev-workbench -->
