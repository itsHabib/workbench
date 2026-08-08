---
name: claim
description: Claim the current session's unit of work (dossier task, Jira ticket, PR, or free text) by appending a claim/link event to the session-claims log. Use when the operator says "/claim <work>", "claim this session", "this session is for X", "track this session against ROX-142", or "link pr N to this session". Opt-in only — never claim without being asked. Spec: workbench docs/features/session-claims/spec.md.
---

# /claim — claim this session's unit of work

Append one JSON line to `~/.claude/session-claims/claims.jsonl` describing what
THIS session is working on. Append-only: never edit or rewrite the file.

## Steps

1. Determine the fields:
   - `session`: this session's id — the UUID in your scratchpad directory path
     (`.../<session-uuid>/scratchpad`).
   - `cwd`: current working directory (absolute).
   - `repo`: `owner/name` from `git remote get-url origin` if in a git repo, else omit.
   - `branch`: current git branch if in a repo, else omit.
   - `worktree`: worktree directory name if cwd is a linked git worktree
     (`git rev-parse --git-common-dir` differs from `--git-dir`), else omit.
   - `work`: parse the argument:
     - `pr <n>` or a PR URL → `{"kind":"pr","id":"<n>"}` and event `link`
       (a PR added to an already-claimed session is a link, not a new claim;
       if the session has no prior claim this session, still use `claim`).
     - Jira-style key (`ABC-123`) → `{"kind":"jira","id":"ABC-123"}`.
     - `dossier <project/phase/task>` → `{"kind":"dossier","id":"..."}`.
     - Anything else → `{"kind":"free","id":"<the text verbatim>"}`.
   - `ts`: current UTC time, RFC3339.
2. Append (create dir/file if missing), single line, via:
   ```sh
   mkdir -p ~/.claude/session-claims && printf '%s\n' '<the JSON>' >> ~/.claude/session-claims/claims.jsonl
   ```
3. Confirm to the operator in one line: `claimed: <work> (session <first 8 chars>…)`.

## Event shape

```json
{"ts":"2026-08-06T14:02:11Z","session":"<uuid>","event":"claim","work":{"kind":"jira","id":"ROX-142"},"repo":"itsHabib/roxiq","branch":"claude/search-p1","worktree":"epic-jennings","cwd":"/Users/mh/dev/roxiq"}
```

`event` is `claim` or `link`. Re-claiming appends a new event — no mutation, ever.
No fields beyond these. If not in a git repo, omit repo/branch/worktree and proceed.
