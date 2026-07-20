**Status**: draft
**Owner**: @mh
**Date**: 2026-07-20
**Related**: extends [session-engine-skill](../session-engine-skill/spec.md) (P4) and the locked [driver-state](../driver-state/spec.md) plane (§4 D4, §5, §6, §7 F1/F3). Prior art: the P4 skill variant executed impl **inline**; this phase makes it **thin**.

# session-orchestrator — the thin `--engine session` driver — design spec

## 1. Problem & hypothesis

`/work-driver --engine session` makes the current session BE the driver engine: it runs the
state machine and, per task, executes worktree → impl → PR → gate **inline in its own
context**, recording every transition through `workbench-mcp`. The P4 spec declared a hard
ceiling of **N≤3 streams**, and the cause is specific: the engine session holds the FULL impl
context — every file read, every diff, every test-output tail — for EVERY task in one window.
Orchestrator context grows `O(Σ diffs)`. Past ~3 medium tasks, summarization churn degrades
the orchestrator itself: it starts forgetting which stream is where, and the ledger it writes
drifts from the work it did.

**Hypothesis.** Move each task's impl into a **delegated subagent** with its own isolated git
worktree, fresh context, and model (`Agent(isolation:"worktree", model:<tier>)`). The parent
holds only task state + the child's **structured summary** + the PR URL. Heavy context lives
and dies in the child. Orchestrator context then grows `O(#tasks)` in summaries, not
`O(Σ diffs)` — so the same session can drive ≥5 tasks with no summarization pressure, and the
run stays resumable from the ledger in a fresh session.

The P4 ceiling is not relitigated — it was correct for an *inline* engine. This phase changes
the engine's shape so the ceiling moves, not the declaration that an inline engine has one.

## 2. Functional & non-functional requirements

**Functional**
- F-1. Per task, impl runs in a subagent with an isolated worktree, fresh context, and a
  per-task model tier. The parent never reads the child's raw impl context — only its
  structured return.
- F-2. The parent run **owns N child sub-runs** in the ledger; children nest under it by an
  explicit parent ref, so a listing can enumerate a run's children.
- F-3. **Resume across the parent/child boundary:** a fresh session rebuilds orchestrator
  state — which children are done, which PR each produced, which are mid-flight — from the
  ledger alone, WITHOUT re-reading any child's impl context.
- F-4. **Verify the parent by its children:** the parent's per-stream mirror is cross-checked
  against each child sub-run's own recorded PR + terminal status, not re-derived from git.
- F-5. **Per-child friction capture:** gate-loop count, dispatch retries, and worktree
  conflicts are recorded and surfaced per child.
- F-6. **Reconcile before dispatch (git is truth):** before dispatching a task the engine
  cross-checks live git state (matching branch, open PR — by ticket id / touched files) and
  **adopts-or-flags** a task whose work already exists, rather than trusting tracker status.
- F-7. **Pluggable done-boundary:** a per-run policy `green | pr-open | merged` decides how a
  stream reaches DONE. Local-only, human-merge repos run `pr-open` with no cloud dispatch and
  no bot reviewers; nothing bakes in a cloud-reviewer or auto-merge dependency.
- F-8. **Recording ergonomics:** the engine records a transition by naming a kind, a stream,
  and the facts — the server mints the id and time and constructs the body. The engine LLM
  never reconstructs the wire format (JSON tags, per-kind schemas, `evt_`/`dsr_` id minting,
  whole-second timestamps).

**Non-functional**
- N-1. **No new state-machine transitions.** The parent is an ordinary driver run; children
  are ordinary driver runs. The merged §5 transition table and §6 reducer are untouched — this
  phase is additive optional fields + read verbs + one ergonomic record verb + skill prose.
- N-2. **Contract stays v0.1.0.** Every field added is optional (Go `omitempty` ⟺ schema
  optional). Existing ledgers, canonical vectors, and the cross-language chain rule are
  unaffected. A version bump would break the `v` const gate for live runs and is not taken.
- N-3. **Concurrency isolated after import.** Children each write their OWN sub-run ledger
  under their OWN lease, so once imported they never contend on each other's files. The one
  shared point is the state-root `import.lock`, which every `run_imported` takes across its
  cross-run dedupe scan — so child *creation* serializes briefly (bounded lock contention),
  then runs fully independent. The parent lease guards only the parent ledger, which the
  parent writes serially as returns arrive.
- N-4. **Single writer per ledger** is preserved everywhere — the lease law is unchanged; it
  is simply applied per-sub-run instead of once for the whole batch.

## 3. Architecture overview

Two ledger altitudes, joined by an explicit link:

```
parent run  dsr_P        (orchestration altitude — O(#tasks), survives, resumable)
  run_imported {streams:[dss_a, dss_b, …], done_boundary, engine:"session-orchestrator"}
  stream_dispatched dss_a {child_run: dsr_Ca}         ← the join link, from the child's return
  stream_attempt    dss_a {seq:1, terminal, commit}   ← one terminal attempt = the child's head (the state machine needs landed before pr_open)
  stream_pr_opened  dss_a {pr, url, head_sha}         ← mirrored from the child's return
  stream_merged     dss_a {pr, merge_commit, …}       ← only when done_boundary = merged
  … per stream …
  run_finished                                        ← only when all streams terminal (merged/skipped)

child sub-run  dsr_Ca    (impl altitude — O(diff), disposable, written by the subagent)
  run_imported {repo, source, parent: dsr_P, parent_stream: dss_a, streams:[dss_a]}
  stream_dispatched dss_a {engine:"session-orchestrator-child", worktree, branch, worktree_conflict}
  stream_attempt    dss_a {seq, doc_path, terminal, commit}     ← retries = extra attempts
  stream_pr_opened  dss_a {pr, url, head_sha}
  review_cycle      dss_a {cycle, panel_settled, findings}      ← gate-loop count = # of these
  (stream_merged only if the child itself merges — pers/ merged-boundary runs)
```

- The **parent mirror** is a strict SUBSET of each stream's lifecycle — dispatched, pr_opened,
  (merged) — that serves as the join key and keeps `driver_state <parent>` meaningful and the
  parent `run_finished`-able through the *existing* machinery. It is not a duplicate of the
  child at the same altitude: the child carries attempts, retries, review cycles, friction that
  the parent never sees.
- The **child sub-run** is the disposable detail. It can be pruned later without touching the
  orchestration record. Its lease isolates it from every sibling.
- **`driver_rollup {run: dsr_P}`** is the join: it reads the parent's stream mirror and each
  linked child's reduced state and returns one row per stream — the resume roster (F-3), the
  parent↔child cross-check (F-4), and the friction rollup (F-5) in a single read that never
  touches impl context.

Boundary law respected: the child subagent and the parent share only the driver-state
**contract** (types + schema), never call stacks. The child records through the same MCP
surface; the parent reads through it. No tool imports another's decision code.

## 4. Key decisions & trade-offs

**D1 — Children are sub-runs, not streams-in-the-parent.** (Resolves the goal's requirement 1.)
Each task's impl is its own driver run linked upward by `parent`/`parent_stream` on its
`run_imported`. Alternative considered: children as plain streams of the one parent run,
recorded from subagent returns. Rejected because (a) N parallel children would contend on the
single parent lease (N-3), and (b) fine friction (attempts, review cycles) would bloat the
parent ledger, defeating the `O(#tasks)` goal. Sub-runs give each child its own lease and its
own disposable detail file. Cost: a join step (the rollup) and a coarse parent mirror. Worth it.

**D2 — Coarse parent mirror over a state-machine change.** The parent records
`stream_dispatched → stream_pr_opened → (stream_merged)` per stream from the child's return, so
the parent is an ordinary run and `run_finished` works unchanged (N-1). Alternative: relax
`run_finished` legality to "all children reached the boundary." Rejected — it edits the merged
core state machine for no reader benefit the mirror doesn't already give. The mirror is a
subset at a different altitude (§3), and the rollup's cross-check (D5) is exactly the guard
against mirror/child drift.

**D3 — Deterministic, content-addressed event ids for the ergonomic verb.** (Requirement 6.)
The P4 recording loop demanded the engine mint `evt_<32hex>` client-side and *reuse it
verbatim on retry* — the idempotency key `Append` dedupes on. Hand-driving that (plus
whole-second timestamps, plus per-kind body JSON tags) was the single biggest recording
friction. The fix: `driver_transition` takes `{run, kind, stream, actor, facts}` and the server
derives the id **deterministically from the transition's natural key** (§6). A retry with
identical facts maps to the SAME id → `Append` dedupes → returns the original. This is strictly
safer than client-minted random ids: the LLM cannot *forget* to reuse the id, because the
identity IS the facts. `driver_record` (the low-level, caller-supplies-the-event verb) stays for
resume/reconcile paths that must write a specific pre-shaped event; new work uses
`driver_transition`. Two verbs, one `Append` mechanism.

**D4 — Friction is derived, not a new event kind.** Gate-loop count = number of `review_cycle`
events on the child; dispatch retries = extra `stream_attempt`s (len(attempts) − 1); worktree
conflict = one new optional flag on `stream_dispatched`. No new kind, no enum churn. The reducer
folds `review_cycle` count and the conflict flag into `StreamRecord` (both currently dropped),
and the rollup computes retries. Minimal surface, and it closes a real reducer gap (review
cycles vanish from the reduced view today).

**D5 — `driver_verify` stays chain-integrity; the rollup carries semantic verify.** Requirement
3 ("verify the parent by each child's PR + gate status") is a *cross-store* check, not a hash
check. `driver_verify` remains the per-run chain verifier. The rollup adds `agrees: bool` per
stream — the parent's mirrored PR/status vs the child's own recorded PR/status — so a parent
that recorded ahead of a child's facts (recorded ahead of reality, the failure this plane
exists to kill) is visible without re-deriving anything from git.

**D6 — Reconcile is against git, recorded into the ledger.** (Requirement 5, the baseline
lesson.) In the baseline run the tracker said "backlog" while the work was already implemented,
pushed, and open as a draft PR; the run was saved only by an ad-hoc "list worktrees" step. The
rule this phase bakes in: **before recording `stream_dispatched` for any task, the engine
queries live git** (`git ls-remote --heads`, `gh pr list` matched by the task's branch
convention / ticket id / `touches`). A match is **adopted** — the existing branch/PR becomes the
stream's dispatch, recorded as such — never re-dispatched. Trusting tracker status is the
failure; git is truth. This is orchestrator policy (skill prose), because the truth source is
git, not the ledger — but it is a *hard* pre-dispatch gate, not advisory.

**D7 — Done-boundary is orchestrator policy over a fixed terminal vocabulary.** (Requirement 7.)
`done_boundary ∈ {green, pr-open, merged}` rides the parent `run_imported` body so resume knows
it. It decides how far the engine pushes each stream and whether it attempts `run_finished`:
- `merged` (pers/ default): drive through the gate → merge; parent records `stream_merged`;
  all merged → `run_finished`.
- `pr-open` (local-only / human-merge default): drive to an open PR and STOP. No cloud
  dispatch, no bot reviewers, no gate merge. The stream sits at `pr_open` (a non-terminal
  status), so the parent run legitimately stays `open` — the merge happens later, by a human,
  outside the ledger. Never fabricate `run_finished`.
- `green`: like `pr-open`, but the engine additionally waits for the repo's local gate/CI to
  report green before it stops.

No boundary needs a new event or a state-machine change — the terminal vocabulary
(merged/skipped/failed) is unchanged; the boundary only governs the drive. `boundary_reached`
is a derived condition the rollup reports (`all streams ≥ the boundary status`).

**Honest limit of `boundary_reached`.** It reflects LEDGER state, not the world. For `green`
the ledger carries no "CI is green" fact — `green` and `pr-open` both resolve to the `pr_open`
milestone — so `boundary_reached:true` on a `green` run means "every stream reached an open PR,"
NOT "CI passed"; the green wait is enforced by the *drive*, not provable from the ledger.
Separately, `boundary_reached` measures the PARENT's mirror; a mirror that ran ahead of its
child still reports reached, which is exactly why it must be read alongside the rollup's per-
stream `agrees` (a `merged` parent over a `pr_open` child reports `boundary_reached:true` AND
`agrees:false` — the pair is the signal, not either alone).

## 5. Data model (contract deltas — all additive, all optional)

`contracts/driverstate`, staying v0.1.0. Each new Go field is `omitempty`; each new schema
property is optional (not in any `required` list); `additionalProperties:false` is preserved by
adding the property to the schema `$defs`, keeping the conformance parity green.

| Body | New field (json) | Type | Meaning |
|---|---|---|---|
| `run_imported` | `parent` | string | The parent run id for a child sub-run; empty on a parent/normal run. |
| `run_imported` | `parent_stream` | string | The parent stream (`dss_…`) this child implements; empty on a parent/normal run. |
| `run_imported` | `done_boundary` | string | `green` \| `pr-open` \| `merged`; empty defaults to `merged` at read time. |
| `stream_dispatched` | `child_run` | string | The child sub-run (`dsr_…`) this parent stream delegated to. |
| `stream_dispatched` | `worktree_conflict` | bool | The dispatch hit (and resolved) a worktree/branch collision — a friction flag. |

Reducer output (`RunState.StreamRecord`) gains two folded-from-events fields (no contract-body
change — these are derived view fields, already the pattern for `Branch`/`Worktree`):

| `StreamRecord` | `review_cycles` | int | Count of `review_cycle` events folded — the gate-loop count (D4). |
| `StreamRecord` | `child_run` | string | Folded from `stream_dispatched.child_run` — the join link. |
| `StreamRecord` | `worktree_conflict` | bool | Folded from `stream_dispatched.worktree_conflict`. |

`RunRecord` gains `parent`, `parent_stream`, `done_boundary` (folded from `run_imported`), and
`RunSummary` gains `parent` so a listing can filter children.

New mechanism read type (not a contract type — it is a join view, mechanism-level like
`RunSummary`):

```
ParentRollup { run, done_boundary, boundary_reached, streams: [ StreamRollup ] }
StreamRollup { stream, child_run, parent_status, child_status, pr, url, merge_commit,
               agrees, friction: { gate_cycles, retries, worktree_conflict } }
```

## 6. API contract (MCP + CLI deltas)

Additive to `cmd/workbench-mcp` (and mirrored in the `driverstate` CLI). The verb allowlist
grows by two read/record verbs; nothing capability-mutating is added.

**`driver_runs`** — gains an optional `parent` filter: `{repo?, live?, parent?}`. With
`parent` set, returns exactly that run's children.

**`driver_rollup`** (new, read) — `{run}` → `ParentRollup` (§5). Joins the parent's stream
mirror to each linked child's reduced state. This is the resume roster (F-3), the parent↔child
cross-check (F-4, `agrees`), and the friction rollup (F-5). Pure read, no lease.

**`driver_transition`** (new, record) — the ergonomic recorder (F-8, D3):
`{run?, kind, stream?, actor, ext_ref?, facts}`. The server:
1. constructs the kind's `body` from `facts` (typed per kind — the caller passes flat facts,
   never JSON tags);
2. derives the event id deterministically from the transition's **natural key**:

   | kind | natural key |
   |---|---|
   | `run_imported` | `(repo, source, generated_at)` — the existing import dedupe key |
   | `stream_dispatched` | `(run, stream, child_run)` — child_run discriminates a re-dispatch to a NEW child (the state machine allows `failed → dispatched`); a bare re-dispatch with no child_run needs `driver_record` with an explicit id |
   | `stream_pr_opened` | `(run, stream)` |
   | `stream_merged` | `(run, stream)` |
   | `stream_attempt` | `(run, stream, seq)` |
   | `review_cycle` | `(run, stream, cycle)` |
   | `stream_landed`/`failed`/`skipped` | `(run, stream)` |
   | `run_finished` | `(run)` |

   `id = "evt_" + hex(sha256(kind ‖ key))[:32]`, satisfying `Append`'s `evt_` prefix rule;
3. sets `time` server-side (whole-second UTC);
4. appends through the same `Append` path — so a retry with identical facts hits the SAME id
   and `Append` returns the original committed event (idempotent by construction).

   The natural keys are chosen so two *distinct* legitimate events never collide (seq/cycle
   discriminate the repeatable kinds; the one-per-stream kinds key on `(run, stream)`), and a
   *retry* of one event always maps home.

`driver_record` is unchanged and retained for the low-level, caller-shapes-the-event path
(resume/reconcile writing a specific pre-built event).

## 7. Key flows

**Per-task impl contract (the subagent).** The parent dispatches one `Agent(isolation:
"worktree", model:<tier>)` per task with a fixed contract:
- **Input:** one spec path; the target repo + branch base; the repo's local gate command set
  (passed in, never hardcoded); the parent run id + this task's parent stream id + the resolved
  `done_boundary`.
- **Behavior:** mint a child sub-run (`driver_transition run_imported`, carrying
  `parent`/`parent_stream`); create the worktree; implement the spec; run the local gate; open
  the PR (unless boundary is a pre-PR stop); record its own sub-run transitions (dispatched with
  worktree_conflict if any, attempts with retries, pr_opened, review_cycles); STOP at the
  boundary.
- **Output (structured, consumed as DATA — no prose beyond the summary):**
  ```
  { childRun, branch, prUrl, gateStatus: pass|fail, gateLog: <tail>, filesTouched: [...],
    oneLineSummary, friction: { gateCycles, retries, worktreeConflict }, blockers: [...] }
  ```
The parent reads only this. It then mirrors the stream up (`stream_dispatched {child_run}`,
`stream_pr_opened {pr,url,head_sha}`, and — merged boundary — drives the tail and records
`stream_merged`). Heavy context died with the child.

**Drive (parent).**
1. **Grant + credentials first** (unchanged from P4): resolve a live operator-minted gate grant
   for a `merged`-boundary pers/ repo; state which token + gh account a run uses; agents never
   mint. `pr-open`/`green` boundaries on human-merge repos skip gate entirely.
2. **Reconcile before dispatch (D6):** for each task, query live git (branch by convention /
   ticket id, `gh pr list` by touched files). Adopt a match (record its branch/PR as the
   stream's dispatch); flag an ambiguous match to the operator; only a clean no-match dispatches
   a fresh child.
3. **Dispatch children** up to the batch's parallel-safe width (file-disjoint `touches`), each
   an isolated-worktree subagent. Record `stream_dispatched {child_run}` per task as the child
   returns its id.
4. **Consume returns**, mirror each stream up via `driver_transition`, drive the merged-boundary
   tail per the repo's panel/gate policy, and record `stream_merged`. `run_finished` when all
   streams are terminal.

**Resume (F-3, extends §7 F3).** Fresh session: `driver_runs {live:true}` → pick the parent →
`driver_rollup {run}`. The rollup names every stream's `child_status`, `pr`, and whether the
parent mirror `agrees`. For a mid-flight child (dispatched, no PR mirrored): reconcile git
(the child's `stream_dispatched.branch`/`worktree` say where to look) → if the PR exists, mirror
it up; else re-dispatch a fresh child subagent. Never re-read the child's impl context; never
act on ledger state without the git reconcile. A new session actor per generation, as before.

## 8. Concurrency / consistency / failure model

- **Parallel children, isolated after import (N-3).** Each child writes its own `dsr_C*`
  ledger under its own lease. The parent writes only `dsr_P` and only when a return arrives,
  serially. The one shared file across the fan-out is the state-root `import.lock`, which every
  `run_imported` holds across its cross-run dedupe scan — so child *creation* serializes briefly
  (bounded lock contention), but no two siblings ever contend on a data ledger, so `ErrLocked`
  between siblings does not occur. Sibling identity is `(repo, source, generated_at, parent,
  parent_stream)`: children off one manifest share the first three, so the parent linkage is what
  keeps a second child's import from deduping into the first child's run (§4 D1).
- **A child that dies** leaves its sub-run `open` with whatever it recorded (torn-tail tolerant
  as always). Resume reconciles it from git like any other mid-flight stream.
- **Idempotent recording (D3).** Both parent mirror and child detail record through
  content-addressed `driver_transition`, so any retried transition is a no-op returning the
  original — the whole fan-out is at-least-once safe end to end.
- **Mirror/child drift** is detected, not prevented: the rollup's `agrees` flag surfaces a
  parent stream whose mirrored status ran ahead of the child's own record, so recorded-ahead-of-
  facts is caught at read time.

## 9. Rollout / implementation plan

| # | Phase | Goal | Tasks | ~Weighted LOC |
|---|---|---|---|---|
| O1 | contract | additive optional fields + schema parity | `run_imported` parent/parent_stream/done_boundary; `stream_dispatched` child_run/worktree_conflict; schema `$defs` props; conformance cases | ~120 |
| O2 | mechanism | reducer folds + parent filter + rollup | fold review_cycles/child_run/worktree_conflict/parent into RunState/RunSummary; `Runs` parent filter; `Rollup(dir, parent)`; unit tests | ~250 |
| O3 | MCP | `driver_transition` + `driver_rollup` + `driver_runs` parent | ergonomic recorder with deterministic ids; rollup verb; runs param; verbs_test | ~250 |
| O4 | skill | thin-orchestrator prose + per-task contract | rewrite `--engine session` (subagent-per-task, structured return, reconcile step, done-boundary); registry sync | ~120 prose |

> **Scope note.** O1–O3 (this repo's PR) deliver the **plane**: the ledger primitives the
> orchestrator records through. O4 is the **consumer** and lives OUTSIDE this repo — the
> canonical skill is `~/.claude/skills/work-driver/SKILL.md`, synced to the `pers/skills` /
> `pers/cc-skills` registries, not versioned here. So the subagent-dispatch / structured-return /
> serial-parent-recording *loop* is not in this diff and its end-to-end concurrency claim is not
> provable by this PR's CI — it is validated by the operator-run §11 dogfood gate. This PR's CI
> proves the primitives (contract, reducer, rollup, verbs) in isolation.

O1→O2→O3 are the Go plane; O4 is prose consuming it. All in one feature branch/PR (the phases
are one coherent change and the skill is prose, per the bigger-PR policy).

## 10. Open questions

1. **Child model tier default.** The per-task contract passes `model:<tier>`; the default
   mapping (task risk/size → tier) is left to the skill's judgment for v0 rather than pinned
   here. Revisit if a fixed policy proves better than per-task judgment.
2. **Rollup depth.** v0 rollup is one level (parent → children). Nested orchestration (a child
   that is itself a parent) is out of scope and not anticipated — the N≤3 → ≥5 move does not need
   it.
3. **Child ledger pruning.** Child sub-runs are described as disposable; an actual GC policy
   (prune children of a `finished` parent past some age) is deferred — the ledgers are small and
   nothing forces it yet.

## 11. Validation plan

Binary, dogfood-style (mirrors the P3 §11 gate). **One `--engine session` run drives ≥5 real
tasks to PR-ready** where:
- (a) each child ran in its own isolated worktree with **no cross-task file collision**, and the
  orchestrator showed **no context summarization** across the whole run;
- (b) every parent stream mirror and every child sub-run detail was recorded through
  `driver_transition` (`driver_verify` ok on parent and every child throughout);
- (c) `driver_rollup {parent}` reports all ≥5 streams reaching the run's `done_boundary`, with
  `agrees:true` on every stream;
- (d) a mid-run kill (placed between a child opening its PR and the parent mirroring it) resumes
  in a fresh session via `driver_runs {live}` → `driver_rollup` → git reconcile → mirror the
  missing event, WITHOUT re-reading any child's impl context;
- (e) a `pr-open`-boundary local-only repo run drives to N open PRs with no cloud dispatch, no
  bot reviewers, and no gate call, leaving the parent run legitimately `open`.

Pass → the thin orchestrator is the default `--engine session` shape and the N≤3 ceiling is
retired (replaced by "≥5, single writer per sub-run"). Fail on (a) or (d) → the isolation or the
resume model is wrong; stop and redesign before widening use.
