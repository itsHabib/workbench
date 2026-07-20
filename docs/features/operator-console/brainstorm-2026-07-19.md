# Operator console — overnight brainstorm (2026-07-19)

Operator prompt: "giant gate commands I always have to run… one unified UI… something
to help me with tasks I need to do — minting, judging, etc." Brainstormed via three
competing perspectives (minimalist / product / architecture), then synthesized.
Companion mockup: the "gate — the docket" Artifact (clickable concept, trace-view
design language).

## Ground truth that shaped everything

- The "UI I love" is **gate's own trace view** (`cmd/gate/docs/demo/trace-view.html`,
  emitted by `gate explain -html`). tracelens has no UI at all — it's a pure CLI.
- **controlroom already happened**: a read-only local web dashboard, built, audited,
  then deliberately removed from main (`e5aaaf0`). Its removal commit says: if a board
  is ever wanted again, rebuild it *flare-shaped (a read-only sink), not as a platform*.
  Its audit documents the null-collections bug mechanism + the lesson that its
  Playwright mocks drifted from real serialization.
- The daily pain decomposes into exactly three things:
  1. **Flag-tail tax** — `-state ~/pers/gate/state -key <dir>` on every command.
  2. **ID shuttling** — copying `grt_…` / `run_…` between commands.
  3. **"What needs me?"** — parked runs and expiring grants are invisible without
     reading `log.jsonl`.
- An open design brief (`~/pers/goal-trace-view-design-2026-07-18.md`) already sets the
  aesthetic register: *audit-trail seriousness, not dashboard flash*; "it renders; it
  never decides."

## The synthesis — one staircase, each step valuable alone

### Step 1 — kill the flag tail inside gate (~15 lines, an afternoon)
`GATE_STATE` / `GATE_KEY` env-var defaults in `commonFlags` (`cmd/gate/main.go:242`),
flags still override. Precedent exists (`defaultKeyDir`, `defaultFloorBin`). Also
defuses the wrong-cwd mint hazard `checkGrantStateDir` guards against. Do this
regardless of everything else.

### Step 2 — `gate next`: the inbox as a gate projection (~250 lines, 1–2 days)
New read-only verb beside `explain`/`audit`: project the log into "what needs the
operator," each item with a **paste-ready command** (the grant ID for the suggested
`judge` command comes from the run's own artifacts — no more ID shuttling):

```
PARKED  run_7f3  acme/widget#212  parked_for_judgment  2h ago
        → gate judge -run run_7f3 -grant grt_9ab -decision pass -why "..."
        → gate explain -run run_7f3 -html
GRANT   grt_9ab  acme/widget  T2  expires in 3h
nothing else needs you.
```

Derivation: escalation artifacts with no subsequent judgment in the run; grant bodies
vs `expires_at`. A structural projection like `observe` already does — no decision
logic. Add `-json` so any consumer (the console, scripts) gets the same projection:
**gate owns "what needs me"; everything else renders it.**

### Step 3 — the console: new tenant `cmd/console`
Binary/tenant name: **`console`** (operator decision 2026-07-20). No poetic overall
name — the five surfaces are the whole vocabulary, plainly named:
**docket · case file · mint desk · record · runs**. A local, loopback-only web console
in the trace-view design language. (The courtroom words gate already uses — verdict,
escalation, judgment, parked — carry over because they're gate's real vocabulary, not
added flavor.)

Surfaces:
- **docket** (front page): runs awaiting judgment (question verbatim, serif,
  oldest first — chronological, never "priority"), grants ledger with absolute +
  relative expiry, recent decisions strip. Audit is not a screen: `chain intact ·
  audited 09:41` lives in the masthead everywhere; tampering becomes a full-width
  banner that cannot be dismissed.
- **case file** (`/run/<id>`): the existing trace view embedded whole (same
  renderer, rail, provenance arcs) + pinned escalation question + the **judgment
  form** — pass/block radio (neither preselected), required serif "why"
  ("this sentence becomes part of the permanent record"), the exact `gate judge …`
  argv live-echoed as you type (the command echo IS the consent mechanism), submit
  disabled until decision + why exist. Quieter secondary button: refer to auto-judge.
  Four gestures from parked to judged. After submit, the judgment + action artifacts
  appear in the trace above — you watch your judgment enter the record.
- **mint desk** (`/mint`): deliberately slower than the judgment form — issuing
  authority should have more friction than exercising it. Tier radio with one-line
  glosses + the ladder law stated at the point of choice ("a judgment pass cannot
  launder a tier past the ceiling"); TTL always shows the resulting absolute local
  expiry; `-init` surfaced only when the state dir is actually fresh; button label:
  "mint grant"; footer note: minting is constitutionally a human act — this console
  exposes no API for it, only this button.
- **record** / **runs** (later): the full ledger rendered in trace-view idiom, filters
  that are facts (repo/kind/run/date); read-only driverstate lane under **runs**.

Architecture (the architect's verdicts, all justified in detail in the transcript):
- **New tenant, not controlroom revival** (removal commit's own guidance), **not
  `gate serve`** (gate holds key custody — worst possible place for a listener; the
  no-daemon decision stands), **never MCP-reachable mint** (workbench-mcp's
  compile-time verb allowlist deliberately excludes capability mutation — an MCP
  console would reverse that).
- **Read side**: shell `gate next -json` + `gate explain -json` — the console is a
  pure renderer/typist over gate's own projections; hand-parses at most the
  `contracts.Envelope`/`Grant` shapes. (Mirror the Grant body shape into `contracts`,
  types only — the same precedent as `contracts.Verdict`.)
- **Write side**: always shell the gate binary — hash chain, locking, signing, and
  `checkGrantStateDir`-class guardrails stay canonical; the console never sees key
  material. Every response echoes the exact argv; every action lands in the console's
  own append-only journal.
- **Security posture**: salvage controlroom's ~200 lines of host-pin/Origin/CSRF
  scaffolding (`.claude/worktrees/feature/KICKOFF/cmd/controlroom/internal/web/server.go`);
  add a two-step confirm on mint. Adopt the two null-bug rules day one (normalize
  nil→[] at the publish boundary; never swallow render exceptions). Stdlib only; no
  Playwright — Go `httptest` + golden fixtures incl. a null-collections fixture.

### Scope walls (so it doesn't die of interpretation like controlroom)
No scores/health/trends; no recommendations or preselected decisions; no agent-facing
API; no state editing ever (a matter leaves the docket only by being judged); no
notification engine; every action echoes its CLI command — if the console vanished
tomorrow the operator lost convenience, not capability; no cross-tool sprawl.

## Phasing

| Phase | Ships | Risk |
|---|---|---|
| 1 | `GATE_STATE`/`GATE_KEY` env defaults | trivial |
| 2 | `gate next` (+ `-json`) | read-only projection |
| 3 | `console serve` v0: docket + case-file drill-down, **copy-paste commands only** (zero mutating endpoints) | zero attack surface |
| 4 | wire judge (lowest-risk action: needs an existing grant; gate re-checks capability) | CSRF plane lands here |
| 5 | wire mint (two-step confirm) | the human-act surface |
| 6 | record + driverstate lane — only if friction-logged | guard vs sprawl |

Phases 1–2 are worth doing even if the console never happens; each later phase is
individually abortable. The controlroom removal critique ("did not trace to any
logged friction") gates every pane after phase 4.

## Decisions

- **2026-07-20:** operator approved steps 1–3. Binary/tenant name is **`console`**.
  Surfaces named plainly: **docket · case file · mint desk · record · runs** — no
  overall poetic name, no "the bench"/"this is your signature" flourishes.

## Open questions for the operator

1. Does `gate next` live in gate (recommended: gate owns the projection) or derive
   console-side from `contracts.Envelope`?
2. How far down the staircase in the first build — 1–2 only, or straight through the
   console v0 (phase 3)?
3. driverstate **runs** lane in v0 scope or strictly later?
