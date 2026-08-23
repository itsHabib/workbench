# Friction teardown — the rooms#99 merge session (2026-08-04)

Session notes taken live. Dossier phase: `workbench` → `friction-teardown-2026-08-04`.

## What the session was

A TDD review sweep across six design docs (rooms snapshot-fork-replay, roll-call v2,
rung v1, workbench #205 + #186, dossier #104), then driving two PRs to merge.

Both merged: **orchestra#48** (`c593fb1`) and **rooms#99** (`812cc97`).

## The headline number

rooms#99 was green on all five CI checks, had three review cycles with sixteen
findings addressed, and still cost:

- **4** `gate gate` runs
- **2** blocked auto-judgments
- **1** broken judge provider
- **1** exhausted grant ceiling, re-minted mid-merge
- **1** operator override

None of that friction came from the code. The delivery loop itself held —
worktree → implement → check → PR → panel → re-verify worked exactly as
documented, and the bots found real bugs every cycle.

## What actually worked (do not "fix" these)

- **The merge boundary refused a head nobody had reviewed.** Twice, from an
  independent provider, with correct reasoning. That is the system working.
- **Gate's stale-comment exclusion was right.** It correctly excluded 6 stale
  comments from earlier cycles and surfaced exactly the 2 that were live.
- **The panel earned its keep.** Cycle 1: 11 findings. Cycle 2: 3. Cycle 3: 2,
  including a P1 (concurrent `--out` collision) in code the agent had just
  written. Every finding was legitimate.
- **`grant_cycle_exceeded` named its own remedy.** The only terminal state all
  session that told the operator what to do next. Use it as the reference shape
  for `readiness.Escape`.

## The friction, in the order it bit

1. **Panel structurally unreachable.** Cursor Bugbot hit a usage limit and could
   not review *any* head. `completed=0 expected=3` forever → every portfolio PR
   parks on panel-completeness until the quota resets. → task
   `gate-panel-unavailable-vs-missing`
2. **`-provider claude` is dead.** `judgment_malformed: unknown field "findings"`.
   Codex worked on the identical run. One of two independent-judge paths gone,
   with no redundancy left. → task `gate-judge-provider-claude-schema` (chip
   `task_64c101f0` already dispatched)
3. **Consumption of the one-shot was invisible.** After that error, nothing said
   whether the judgment slot was burned. Resolved by grepping
   `~/dev/gate/state/log.jsonl` for a `judgment` artifact. → task
   `gate-judgment-consumption-legibility`
4. **Terminal states named no escape route.** Three of them. → task
   `gate-terminal-escape-routes` (= P1 of the UX overhaul doc)
5. **`gate next` suggested an impossible verb.** Printed a `judge` command for a
   run that had already been judged; `gate resolve` never appeared, and its five
   mandatory flags plus open-park precondition were only discoverable from Go
   source. → task `gate-next-verb-accuracy`
6. **`-max-cycles` counts gate invocations, not review cycles.** Four runs, only
   one of which followed an actual review cycle. Blocked the operator's already-
   accepted decision and forced a mid-merge re-mint. → task
   `gate-grant-cycle-semantics`
7. **Addressed threads look identical to ignored ones.** Two `outdated=true,
   unresolved` P1s; confirmed fixed only by reading the current head's source.
   → task `review-thread-addressed-vs-ignored` (= P5, stays gated)
8. **`make check` permanently red on macOS.** Two host-shell tests. Cost a beat of
   "is this mine?" twice in one session. Fix is open as a *draft* — rooms#100.
   → task `land-rooms-macos-check`
9. **19 runs in "ready to merge", unaged and unswept.** orchestra#48 sat green
   and reviewed for two days until someone asked. → task
   `gate-ready-to-merge-aging`
10. **The friction verb does not exist.** `command -v friction` → not on PATH.
    Every item on this page was captured by hand in a chat transcript, which is
    precisely the failure mode the UX overhaul doc's §4.6 predicts. → task
    `friction-capture-verb`

## The through-line

`docs/features/workbench-ux-overhaul/spec.md` (PR #205) predicted the *shape* of
most of this before it happened: Theme 1 (tools that gate their own repair) and
Theme 2 (signals that report confidently and wrongly). This session ran the
experiment on live work and produced reproducible evidence for P0, P1, P2, and P5.

Two findings are **net-new** and the doc does not cover them:

- **reviewer unavailability vs absence** (#1 above) — an external quota making a
  required panel structurally unreachable is a distinct failure class from
  "nobody has reviewed yet", and nothing in the system can express it.
- **grant-cycle semantics** (#6) — a capability-plane ceiling whose counter and
  whose name measure different things.

Both belong in #205's evidence base before that doc locks.

## One design finding recorded elsewhere

Reviewing #205 during the same session surfaced a P1 in the doc itself: the v2
staleness predicate short-circuits on head match (`check.head_sha == pr.head_sha
⇒ covered`), which re-admits the exact false-green (`ship#242`) that D5 exists to
close — PR checks build the merge ref, so head and base are independent
coordinates. Posted on the PR; not a task here.

## Open at session end

- workbench#207 — the one TDD from the sweep never reviewed
- dossier#104, rung#1 — awaiting operator decisions
- ship#235 — two live codex findings
- workbench#196 — needs a rebase
- rooms: `base_repo_sha` deferred to `docs/follow-ups.md`; phase-1 rooms-host
  gate has never been exercised against real Firecracker
