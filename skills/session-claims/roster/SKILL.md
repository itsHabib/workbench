---
name: roster
description: Render the session-claims roster — one table of session → work → PRs → last activity, derived from ~/.claude/session-claims/claims.jsonl plus read-time signals (session transcript mtime, PR state, worktree existence). Use when the operator asks "roster", "what are my sessions doing", "which session owns what", "show session claims", or invokes /roster. Read-only; owns no state, decides nothing. Spec: workbench docs/features/session-claims/spec.md.
---

# /roster — session → work → PRs → last activity

Storeless read-only view. Reduce `~/.claude/session-claims/claims.jsonl`, then
enrich each row at read time. Never write anything.

## Reduce

Group events by `session`. Per session: latest `claim` is the work label
(multiple claims → newest wins for WORK, note the count); all `link` events with
`work.kind: pr` accrete into PRS; a `release` newer than the latest claim closes
the row (drop from default view; show under `--all` with its note).

## Enrich (best-effort — skip any signal that fails, never block)

1. **LAST / liveness**: mtime of the session transcript
   `~/.claude/projects/<cwd-slug>/<session-id>.jsonl`, where `<cwd-slug>` is the
   claim's `cwd` with `/` and `.` replaced by `-`. No transcript → fall back to
   the newest event `ts`.
2. **PR state**: `gh pr view <n> --repo <repo> --json state,mergedAt` per linked
   PR. All PRs merged/closed and no other open claim → mark row `done (unreleased)`
   and drop from default view.
3. **Worktree**: if the claim named a worktree and it no longer exists in
   `git worktree list` for that repo → note `(wt gone)`.
4. **Aging**: LAST > 24h → append `(stale?)`; LAST > 7d → hide from default
   view (visible with `--all`).

## Output

```
SESSION        AGE   WORK                          BRANCH / WORKTREE        PRS        LAST
session_01Un…  2h    ROX-142 pagination phase 1    claude/search-p1 (wt)    #71,#73    4m ago
```

Flag any work unit claimed by two live sessions — the collision the operator
most wants to see. Empty log → say so in one line; don't scaffold anything.
