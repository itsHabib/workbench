# Workbench glossary

The standalone reference for the workbench vocabulary. `docs/workbench-101.md`
is the teaching doc — read it for the *why*; its own §11 glossary is the inline
backstop and these definitions agree with it. Terms are grouped by layer, not
alphabetized: each group reads top-down as a mini-model of that layer.

New here and non-technical? Start with `docs/plain-language-overview.md`
instead, then come back for the precise terms.

---

## The system

- **workbench** — one Go module (`github.com/itsHabib/workbench`) holding a
  family of small single-job binaries that let coding agents ship real PRs
  under control. The organizing bet: safety-critical decisions live in code the
  model cannot skip, not in prose it can. Slogan: *prose shrinks, guarantees
  grow.*
- **plane** — one of five responsibilities the system is decomposed into. A
  *role, not a service*: several tools can serve one plane. **State**
  (remembers), **Execution** (does), **Verification** (judges), **Capability**
  (bounds), **Observability** (explains).
- **Composition** — the sixth, deliberately opinionated layer: the agent plus
  thin policy files (skills) gluing planes into workflows. The only layer
  allowed to know about multiple tools.
- **tenant** — a tool living at `cmd/<tool>/` in the module, guts private under
  its own `internal/`.
- **the boundary law** — *share contracts, not call stacks.* Tools never import
  another tool's decision logic; they compose through artifacts (typed JSON on
  disk + exit codes). Enforced by CI's `hygiene` job, not convention.
- **contracts** (`contracts/`) — the shared vocabulary package: verdict types,
  the artifact envelope, embedded JSON schemas, and pure contract-law
  validation. A **leaf package**: imports nothing else in the module, carries
  no decision logic.
- **mechanism package** — a top-level shared library (`local/`,
  `driverstate/`) that is pure plumbing any tool may call — never a place a
  routing rule or reducer can hide. Leaf-checked like `contracts`.
- **artifact** — a typed JSON record on disk (`evidence`, `verdict`, `grant`,
  `action`, `escalation`, `judgment`, …); with exit codes, the only cross-tool
  channel.
- **seam** — a stable integration boundary callers branch on: exit codes,
  binary names, JSON shapes. "Load-bearing seam" = breaking it breaks callers.
- **MCP server / skill / hook** — the three composition pieces: a *capability*
  (touches real state), a *routine* (the workflow file the agent runs), a
  *reflex* (fires whether the model wants it or not).
- **harness** — the agent runtime a session runs in (Claude Code, Codex, …);
  the thing skills and hooks are installed into.
- **lazy migration** — tools graduate into the module when next touched, never
  as a stop-the-world sweep (`docs/DESIGN.md`).

## The loop

- **the loop** — the end-to-end path of one unit of work: task born in dossier
  → specs + manifest → driver dispatches parallel streams → a PR per stream →
  review panel + coordinator → gate authorizes the merge → merge → record →
  observe. `docs/workbench-101.md` §2 is the canonical statement.
- **dossier** — the work-item store (separate Rust tool): a markdown task
  corpus — project → phase → task — behind a typed MCP surface. State plane.
- **ship** — the driver engine (separate TypeScript tool): runs the durable
  dispatch state machine (import, dispatch, poll, judgment, land) and persists
  runs. Execution plane.
- **stream** — one parallel work item within a driver run.
- **seat** — one agent identity doing work (a Claude session, a Codex task, a
  cloud runner).
- **manifest** — the prep artifact batching which tasks can safely run in
  parallel, computed from file overlap.
- **session engine / thin orchestrator** — the mode where the Claude session
  itself is the engine: each task delegated to an isolated-worktree subagent,
  the parent holding only structured summaries, every transition recorded so a
  fresh session can resume a crashed run.
- **driver-state ledger** — the append-only, hash-chained record of driver-run
  state transitions (mechanism in `driverstate/`, verbs via `workbench-mcp`).
- **merge tail** — the end of a driver run: reviews → gate → merge → record.
- **review panel** — the set of AI reviewers commenting on a PR;
  `/review-coordinator` is the judge over the reviewers, driving address
  cycles.
- **provenance footer** — the `Provenance:` line ending each generated PR,
  naming the seat, model, and pipeline that produced it.

## Verification and the ladder

- **verdict** — the shared judgment artifact: subject + decision + tier +
  producer + findings + confidence. One schema for every verifier
  (`contracts/verdict.go`).
- **decision** — pass / escalate / block: *who may proceed*. Orthogonal to
  tier by design — collapsing the two axes fails on the first real PR.
- **tier (T0–T3)** — *who must approve*: T0 auto (narrow slice), T1 one peer,
  T2 owner (CODEOWNERS), T3 owner + adversarial skeptic + author "why safe"
  defense. Policy in `cmd/triage/RUBRIC.md`, compiled into the floor.
- **producer** — the structured `{class, impl}` pair on a verdict. Class
  (`code` / `local-model` / `judgment`) carries ladder semantics; impl is
  provenance only — the reducer never branches on it.
- **the ladder** — one law, two views. *Model-routing:* free local model for
  clerk work, everyday model as home base, apex model only for genuine
  judgment. *Verifier:* deterministic floor always runs; a local model may
  pass or escalate, **never block**; premium judgment resolves escalations but
  **cannot override a code block**.
- **floor** — the deterministic rung; always runs, can never be lowered.
- **advisory** — a model layer that may only *raise* above the floor, never
  lower or approve.
- **verifiable or escalate-safe** — the three-word routing rule for sending
  work to the cheap rung: either a deterministic check can verify the output,
  or a wrong answer only ever costs an extra cloud call. Either property sends
  the work down; neither keeps it up. Difficulty is never the flag.
- **confident garbage** — the formative failure: a local model labeling real
  bugs false-positive *at confidence 1.0*. Why self-reported confidence is
  recorded but never trusted.
- **reducer** — gate's monotone composition of many verdicts into one: worst
  decision wins, max tier wins, min confidence carries
  (`cmd/gate/internal/verify/verify.go`).
- **fail closed** — unknown or absent input becomes park/refuse, never a
  silent pass. Unknown tiers rank highest; unknown producer classes and
  decisions are rejected; an error is never a tier. "Absence never reads as
  green."

## Authority and the audit trail

- **gate** — the flagship binary: decides whether a PR may merge, from
  evidence, via the verifier ladder and an operator-minted grant, recording
  everything. Verbs: `grant`, `gate`, `judge`, `explain`, `next`, `audit`,
  `backtest`, `stress`.
- **grant** — an operator-minted, HMAC-signed, expiring capability: repo +
  action scope, tier ceiling, review-cycle ceiling, TTL. Checked before
  evidence gathering *and again* at effect time; the ceiling re-applies after
  judgment.
- **exit-code seam** — gate's contract with callers: 0 pass / 1 blocked /
  2 parked / 3 refused / 4 error.
- **park** — the fail-closed stop: exit 2, with the *full* question packaged
  for a judge. Distinct from **block** (red evidence, exit 1) and **refuse**
  (no valid authority, exit 3).
- **judgment** — a frontier model resolving a parked escalation, fed *only*
  recorded artifacts. If a good judgment would need more than the artifacts
  carry, that is a contract bug in the artifacts.
- **escalation** — the agent→human→agent loop for decisions the system will
  not make alone: gate parks with the full question packaged, flare routes
  the notification, a human decides, and `escalate` ingests that decision and
  drives `gate resolve` to close the loop. Deliberately a **contract + seam,
  not (yet) a sixth plane** — the work that set out to build one concluded
  the five planes stand (`docs/features/escalation-plane/spec.md`).
- **run / lineage** — artifacts group by run id and link by a `Parents` field;
  no outcome exists without naming the verdict it acted on and a live grant.
- **hash chain / keyed anchor** — the tamper-*evidence* model: each append is
  content-hashed onto the chain; an HMAC anchor over head + entry count (key
  held outside the state dir) closes truncation/rewrite. Honestly stated:
  evidence, not access control.
- **custody** — credential custody: vendor keys live behind a local proxy and
  are exercised under custody grants (key scope, action set, TTL); agents
  reference `custody:` secret-refs, never raw values.
- **canary** — the one repo where branch protection already requires gate's
  status check.

## The tenants, one line each

- **gate** — merge authorization (see above).
- **triage** — PR risk classification: `triage-floor` (deterministic tier
  floor) + `triage-advisory` (escalate-only model advisory).
- **tracelens** — agent-trace diagnostics; consumed via its CLI exit-code
  seam.
- **flare** — the escalation/block routing sink: tails the logs, pushes
  notifications. Observability, and deliberately nothing more.
- **console** — local, read-only web view of gate's inbox (parked runs + the
  grant ledger); shells the gate binary, never imports it.
- **escalate** — the resolution back-channel: ingests a human's decision on a
  parked escalation and drives `gate resolve`, closing the
  agent→human→agent loop.
- **dispatch** — placement policy as code: deterministically decides where a
  work item runs.
- **workbench-mcp** — the MCP server exposing driver-state verbs to sessions.
- **driverstate** — the CLI over the driver-state ledger (render, rollup, …).
- **runway** — foreground run controller, with a Rooms microVM backend.
- **custody** — the credential proxy (see above).
- **reviewfindings** — the Codex/GitHub producer for the shared
  `ReviewFindingsV1` review-findings contract.
- **local / eval** — CLIs over the `local/` mechanism: structured local-model
  calls with the escalate-on-uncertainty gate, and its eval harness.

## Docs and process

- **verified / intent** — the two status markers used in design docs:
  confirmed against code on `main`, vs designed and written down but not yet
  code. The difference is load-bearing.
- **drift log** — a doc's own honest list of where it and the code disagree,
  in both directions (docs behind code, docs ahead of code). Preferred over
  docs that quietly overclaim.
- **managed block** — a machine-stamped section of a CLAUDE.md/AGENTS.md
  (`<!-- BEGIN … -->`) owned by a generator skill. Edited only at the
  generator; hand-edits are overwritten on refresh.
- **dogfood** — proving a tool by running the portfolio's real work through
  it.
- **local-first offload** — the standing rule: before spending cloud tokens on
  a mechanical sub-step, check for a free local path — and route by
  verifiability, never difficulty.
- **shrink-vs-invest** — the test applied to every piece of the system: *was
  this compensating for a weak model (shrink it), or is it what makes a strong
  model safe to trust longer (invest in it)?* Instruction scaffolding shrinks;
  floors, ladders, grants, and audit logs grow.
