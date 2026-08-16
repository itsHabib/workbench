# Merge-first sweep contract

Task: `gate/sweep-friction-2026-08-12/merge-first-sweep-contract`

The sweep of 2026-08-12 surfaced a recurring failure shape: `/pr-sweep` reports a board,
the operator reads it, and PRs still don't merge. The report was accurate and inert. Ten
friction items came out of that sweep; the seven below are the ones that belong in the
sweep's own contract rather than in `cmd/gate`'s code.

This document is the normative contract. It is written so that §"Skill text" can be
pasted verbatim into `~/.claude/skills/pr-sweep/SKILL.md` — `/pr-sweep` is an operator-level
skill living outside this repo (unlike the vendored `skills/session-claims/*`), so applying
it is a separate, operator-side step.

## Why the contract, not a code change

`cmd/gate` owns *authorization* — may this exact head merge. It does not own the sweep. But
the sweep is the funnel that feeds gate, and every friction item below is a way the funnel
leaks before a run is ever opened: PRs closed instead of merged, blockers listed instead of
named, review *suggestions* mistaken for defects, review cycles requested instead of
judgments taken, merges landed with no ledger row tying head → run → SHA.

A sweep that leaks in those ways manufactures work for gate and then discards gate's output.

## The seven clauses

### 1. Merge-first (friction: closures without a recorded reason)

The sweep's default terminal state for an open PR is **merged**. Closing is an exception
that must carry an explicit, recorded **supersession reason** naming what replaced the
work — a PR number, a commit, or a written decision that the work is no longer wanted.

"Stale", "superseded" with no referent, and "no longer needed" are not reasons; they are
labels for the absence of one. A PR with no recordable supersession reason stays open and
stays on the board.

The sweep itself never closes anything (it is read-only). The clause governs what it may
*recommend*: a close recommendation is malformed unless it carries the reason inline.

### 2. Exactly one blocker, exactly one owner (friction: wall-of-problems rows)

Each PR row names **one** blocker and **one** owner. Not a list, not "and also".

Precedence when several apply — the blocker to name is the one that must move first:

1. Unresolved review findings (a rebase would only have to happen again)
2. Behind / dirty branch
3. Red check
4. Thin or absent panel
5. Missing authorization (nothing to authorize until the above are answered)

Owner is one of: **agent** (mechanical, un-owned work), **operator** (human judgment,
mint, spend, irreversible), **nobody** (in flight — checks running, inside the grace
window). A row with two owners is an unanalyzed row.

### 3. Suggestions and absent Claude credits are not defects (friction: recurring phantom stalls)

Two classes of non-defect keep parking PRs that were ready:

- **Review *suggestions*.** A reviewer comment marked as a suggestion, nit, or optional
  improvement is not a finding. It does not block, does not count toward the fix-round cap,
  and does not make a PR "stopped short". Only defects — correctness, authorization,
  security, contract violations — do.
- **Absent Claude review credits.** When the `claude` reviewer is absent because credits
  are exhausted or the app is unavailable, that is an **explicit recorded waiver**, not a
  thin panel. Record it once, in the row, as `claude: waived (credits)` and evaluate the
  panel against the remaining roster. It must not resurface as a fresh blocker on every
  sweep — a stall that recurs identically across sweeps is a contract bug, not a finding.

A waiver is recorded, not inferred: if the sweep cannot tell *why* `claude` is missing, the
thin-panel blocker stands.

### 4. Two fix-rounds, then judge (friction: cycle-ceiling parks answered with wider grants)

The review-cycle discipline is unchanged and the sweep must not undermine it:

- At most **two** fix-rounds against the panel per PR. Round 1 and round 2 fix every
  verified P1 and anything touching authorization invariants; push once, re-trigger once.
- After round two, **stop fixing**. Residual P2s and nits go to `gate judge` with a written
  why.
- A `max-cycles` park is the **stop signal**. The sweep never recommends another review
  round, a wider grant, or a raised ceiling in response to one.

Concretely, the sweep's recommendation for a PR parked on **residual findings** after two
fix-rounds is a judgment:

```
gate judge -run run_... -grant grt_... -provider claude|codex -auto -state ~/dev/gate/state
```

and then reading the `action` artifact from the **judged** run for the pinned merge command
— never a fresh `gate gate`, which re-evaluates from scratch and re-parks on the same gap.

**A cycle-ceiling park is a different park and does not take this route.** `cmd/gate` parks
`cycle N exceeds grant ceiling M` as an *authorization* gap: the check re-applies after
judgment, so a judgment pass cannot launder a ceiling (`cmd/gate/main.go:784`). Its only
mechanical clearance is re-minting a wider `-max-cycles` grant, which is operator-only mint
authority — and per the review-cycle discipline the agent never asks for one. So the sweep's
recommendation at a ceiling park is neither a judgment nor a grant request: it surfaces the
blown cap to the operator as a **process defect** (the PR looped), owner `operator`, and
stops. The two-fix-round cap exists precisely so this park is never reached.

### 5. The exact ledger (friction: merges with no reconstructable trail)

Every PR the sweep drives to merge gets one ledger row with **six** fields, all exact:

| field | must be |
|---|---|
| PR | `owner/repo#N` |
| reviewed head | the full SHA the panel actually reviewed |
| gate run | `run_...` |
| emitted merge command | the `gh pr merge ... --match-head-commit <sha>` string gate emitted, verbatim |
| merge SHA | the resulting merge commit SHA |
| accepted residual | what the judge accepted, or `none` |

"Verbatim" is load-bearing: a reconstructed or loosened merge command breaks the tie between
the reviewed head and the merged head, which is the only thing the whole flow is protecting.
If the emitted command cannot be reproduced exactly, the row is incomplete and the merge is
not claimable as gated.

Reviewed head ≠ merge SHA is normal (the merge commit is new); reviewed head ≠
`--match-head-commit` argument is a defect.

### 6. Isolated detached worktrees for dirty roots (friction #9)

When a repo root is dirty, the sweep's follow-on work does **not** touch it. Use an isolated
detached worktree:

```sh
git -C <repo-root> worktree add --detach <path> <ref>
```

Dirty roots caused two distinct failures in the 2026-08-12 sweep: work applied on top of
unrelated uncommitted changes, and work abandoned because the agent refused to proceed. The
detached worktree makes both impossible — the root is untouched and the work still lands.

Clean it up when the branch is merged (`/worktree-remove`); leaving worktrees behind is its
own friction class.

### 7. Normalize stale PR metadata before merge (friction #10)

Before a PR is put up for authorization, its metadata must describe what it actually is:

- **Draft flag** — clear it if the work is done. A draft that is finished is an
  operator-invisible blocker; branch protection and the sweep's own banding both treat
  draft as mid-work.
- **Research-sketch language** — titles and bodies carrying `TDD:`, `(draft)`, `spike`,
  `sketch`, `WIP`, or "exploring whether…" when the PR now ships real behavior. The
  language routes the PR into the "needs your decision" band forever, because a design
  draft's merge genuinely *is* a decision.
- **Body claims that no longer hold** — a stated scope the diff has outgrown.

This is normalization, not rewriting history: the diff is authoritative, the metadata is
made to match it.

## Skill text

Paste into `~/.claude/skills/pr-sweep/SKILL.md`. Two edits: replace the `## Rules` section
with the block below, and append the new `## Merge-first contract` section after it.

### Replace `## Rules`

```markdown
## Rules

- **Read-only. No exceptions.** No merges, no grants, no pushes, no `gh pr close`, no
  `spawn_task`. The sweep observes. If the operator wants action, they say so next turn.
- **Merge-first.** The default terminal state of an open PR is *merged*. A close
  recommendation is malformed unless it names an explicit supersession reason — a PR
  number, a commit, or a written decision. "Stale" is not a reason.
- **One blocker, one owner per PR.** If three things are wrong, name the one that has to
  move first (findings → behind/dirty → red check → thin panel → authorization). Owner is
  exactly one of agent / operator / nobody. A list of five problems per PR is a wall, not
  a report.
- **Suggestions aren't defects.** Reviewer comments marked suggestion / nit / optional
  never block and never count as "stopped short". Only correctness, authorization,
  security, and contract violations do.
- **Absent `claude` is a waiver, not a blocker.** If the `claude` reviewer is missing
  because credits are exhausted, record `claude: waived (credits)` in the row once and
  judge the panel on the remaining roster. It must not resurface as a fresh blocker every
  sweep. If the reason is unknown, the thin-panel blocker stands.
- **Never recommend more review cycles.** Two fix-rounds is the cap. A `max-cycles` park is
  the stop signal, not friction — the recommendation is `gate judge`, never a wider grant
  or a raised ceiling.
- **Fresh ≠ lazy.** A PR opened this morning with checks running is in flight. Judging it
  as "stopped short" trains the operator to ignore the band that matters.
- **Blame the state, not the agent.** "Codex thread unresolved at `store.rs:88`" — not
  "the agent was careless". The state is checkable; the adjective isn't.
- **Never suggest a bare merge.** Merges go through the gate flow, full stop. The sweep's
  strongest possible recommendation is a mint request.
- **Pers by default.** Day-job is a collapsed count behind `--scope all`. Never label a
  portfolio repo with the employer name.
- **No process narration.** Don't say "I queried 18 PRs, then checked threads". Show the board.
- **PR numbers, `file:line`, bot names, ages are first-class.** Adjectives aren't.
```

### Append `## Merge-first contract`

```markdown
## Merge-first contract

Four things the sweep must get right for its report to convert into merges rather than
into another sweep. Source: workbench `docs/features/sweep-friction-2026-08-12/merge-first-sweep-contract.md`.

**Residuals go to the judge.** After two fix-rounds, remaining P2s and nits are not more
work. Emit:

    gate judge -run run_... -grant grt_... -provider claude|codex -auto -state ~/dev/gate/state

then read the `action` artifact from the *judged* run for the pinned merge command:

    grep run_... ~/dev/gate/state/log.jsonl | grep '"kind":"action"'

Never re-run `gate gate` to pick the command up — a new run re-evaluates from scratch and
re-parks on the same unresolved gap.

**The ledger is exact.** Any PR driven to merge gets one row, six fields: PR ·
reviewed head SHA · `run_...` · the emitted `gh pr merge --match-head-commit` string
verbatim · merge SHA · accepted residual (or `none`). A reconstructed or loosened merge
command severs the reviewed-head ↔ merged-head tie and the merge is not claimable as gated.

**Dirty repo root → isolated detached worktree.** Follow-on work never lands on top of
uncommitted changes and is never abandoned because of them:

    git -C <repo-root> worktree add --detach <path> <ref>

Remove it once the branch merges.

**Normalize metadata before authorization.** Clear a stale draft flag; strip
research-sketch language (`TDD:`, `(draft)`, `spike`, `WIP`) from a PR that now ships real
behavior; fix body claims the diff has outgrown. Stale metadata parks a ready PR in the
"needs your decision" band permanently — a design draft's merge genuinely is a decision,
so language that says "draft" makes the PR unmergeable by policy.
```

## Not in scope

No `gate` binary behavior changes. The clauses above constrain the sweep and its follow-on
recommendations; the ladder law, the reducer, and the grant/judge contracts are unchanged.
