**Status**: draft
**Owner**: @mh
**Date**: 2026-07-17
**Related**: locked design [docs/features/driver-state/spec.md](../driver-state/spec.md) §4 D4/D6, §6, §7 (PR #47); §11 P3 validation-gate verdict (this PR); dossier project `workbench`

# /work-driver `--engine session` — the P4 skill variant + panel-from-config — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Skill prose | `~/.claude/skills/work-driver/SKILL.md` (canonical, outside this repo) + registry sync | ~150 | — |
| Panel config | `.ship.json` `review` key — schema documented here; explicit stanza per personal repo (~10 lines each) | ~10/repo | — |
| Repo files | this spec doc + workbench's own `.ship.json` | ~15 | ~15 |

No Go changes. The plane (P1–P3) is the mechanism; this phase is pure policy prose + per-repo config.

## Goal

Let a Claude Code session BE the driver engine for small runs: execute everything itself
(worktree, impl, PR, reviews, gate tail) while recording every transition through
`workbench-mcp`, so run state lives in the ledger — never in conversation context — and any
fresh session can resume via `driver_state` + F3 reconcile. Move the review panel out of
skill prose into per-repo contract (`.ship.json`), per D6.

## Declared scope (D4 — restated, not relitigated)

- **N≤3 streams, single writer per run.** The lease enforces it: `ErrLocked{Holder}` = fail
  fast and report the holder — no queueing, no lease-stealing.
- Needing tick leases or orphan re-attach is the signal the run belongs on **ship's engine**
  (`--engine ship`, the default) — not a feature request against this variant.
- Grant resolution comes FIRST: resolve a live, operator-minted gate grant before dispatching
  anything; absent/expired → park and emit the exact `gate grant …` mint command for the
  operator. Agents never mint.

## Behavior

### The recording loop (F1)

Every transition is recorded via `mcp__workbench__driver_record` immediately after the
external act it describes — the ledger is written from facts, never ahead of them:

| Step | Event | Body carries |
|---|---|---|
| manifest resolved | `run_imported` | repo, source, generated_at, manifest snapshot, stream list |
| worktree + impl started | `stream_dispatched` | engine:"session", worktree, branch |
| commit landed | `stream_attempt` | seq, doc_path, terminal, **commit** (the head SHA — load-bearing for F3 reconcile) |
| PR opened | `stream_pr_opened` | pr, url, head_sha |
| each panel round settled | `review_cycle` | cycle, panel_settled, findings, panel, verdict |
| PR merged | `stream_merged` | pr, merge_commit, merged_at |
| all streams terminal | `run_finished` | — |

Actor convention (validation-gate finding): `session:<label>-<n>` where `<label>` names the
drive and `<n>` increments per session generation — a resumed session MUST append under a
new actor (`…-2`, `…-resume`); two actors on one run is the audit trail working. Event ids
are server-minted when omitted — omit them.

### Resume (F3)

A fresh session resumes with: `driver_runs {live:true}` → `driver_state <run>` →
`driver_verify <run>` → **reconcile external facts before any write** (branch exists? PR
state? merge commit? — `stream_dispatched`'s branch/worktree + `stream_attempt`'s commit
say where to look) → record missing events (idempotent) → continue the drive. Never act on
ledger state alone; never record an event whose external fact you did not verify. An
`ErrIllegalTransition` rejection means re-read `driver_state` and reconcile again — the
contract correcting the agent (F2) is the plane working, not an error to force past.

### Panel from config (D6)

The reviewer set is read from the target repo's `.ship.json` at drive time — never from
skill prose:

```json
"review": {
  "panel": [
    {"name": "codex",   "trigger": "mention"},
    {"name": "claude",  "trigger": "mention"},
    {"name": "cursor",  "trigger": "mention"},
    {"name": "copilot", "trigger": "reviewer-request"}
  ],
  "require": ["codex", "claude", "cursor"],
  "settle_minutes": 15
}
```

- `trigger`: `mention` → standalone `@<name> review` comment; `auto` → the bot fires on PR
  open, post nothing; `reviewer-request` → `gh pr edit --add-reviewer`.
- **Settled** = every `require` member reported, or its `settle_minutes` budget expired
  (record the cycle as degraded, naming the silent bots).
- **No implicit default (operator decision 2026-07-16):** absent or empty `review` key = NO
  automated review step — no pings, no consolidation — and the drive records
  `review: unconfigured` in the `review_cycle` body so the omission is visible. Never paper
  over with a remembered panel; discipline means following the contract that's there.
- Rollout: each personal repo carries the explicit four-bot stanza (workbench's lands with
  this PR); work repos declare what they truly have (e.g. `coderabbit`/`auto` only).

### Merge tail

Unchanged from the base skill: consolidate on real findings (`/review-coordinator`), gate
authorize per PR (`gate gate -repo <r> -pr <n> -grant <grt_…> -state ~/pers/gate/state`),
branch on exit code with code↔JSON agreement, judge only content escalations, re-mint only
via the operator. Record `review_cycle` per settled round and `stream_merged` only after
the merge fact is readable from GitHub.

### Thin jira-epic ingestion (the work demo's mapping step)

A mapping step, not a platform: given a work epic, materialize its selected tickets as
dossier tasks so a driver manifest can be prepped from them.

1. Fetch the epic's child tickets (whatever surface the work Jira offers — CLI, export, or
   paste); take the N≤3 smallest real ones.
2. One dossier task per ticket: title = ticket key + summary, body = description +
   acceptance criteria verbatim, note = ticket URL. Project/phase on the WORK side's
   dossier — ticket content never crosses into pers/ files or memory.
3. Hand to `/work-driver-prep` as usual. That's the whole feature.

## Acceptance

- `/work-driver --engine session <task>` drives a real task with every transition recorded
  (the §11 validation run IS the reference execution; the skill text must reproduce it).
- A kill at any point resumes in a fresh session from ledger + external facts alone.
- The panel used on a drive is provably the target repo's `.ship.json` stanza; a repo with
  no `review` key gets no pings and a `review: unconfigured` record.
- A work epic's tickets can become dossier tasks with no code written.

## Follow-ups (tracked, not this PR)

- Promote `stream_attempt.commit` into the §5 contract payload (validation-gate finding —
  off-contract today, tolerated by decoding, load-bearing for reconcile).
- Document (or enforce) the event-id shape: spec says `evt_<ulid>`, server mints
  `evt_<32hex>`, append validates neither.
- Minimal `driverstate render` (read-only pretty-print of `Reduce`) — §11(c) shipped on
  `state --json` + GitHub diff instead.
- Four-bot stanza rollout to the remaining personal repos' `.ship.json`.
