# Running a fleet of coding agents

One lead session, a few sub-leads, and a lot more builder sessions running in parallel from one desktop. Works in Codex or Claude Desktop.

Three levels, each one a step up from the last. Start at level 1 and move up when the level you are on stops keeping up. Names, group layout, file paths, and cadences throughout are examples. Use whatever fits how you already work, as long as every session knows who it reports to.

## Level 1: one lead and its builders

Enough for a handful of parallel builds.

1. **Open the lead.** One session on the repo. The root checkout is a convenient place for it, since the lead never edits code, but any checkout works. Title it so you can find it, for example `<topic> lead`. Give it a prompt that re-runs on a timer or on every incoming message. In Claude Desktop:
   ```
   /loop 10m you are the lead for "<topic>". builders will message you when they are stuck or
   have status. keep a board at docs/experiments/BOARD.md: one row per builder with branch,
   last push, state, and blocker. answer builders from the board and from the repo. escalate
   to me only for blockers, big architecture decisions, or anything outward-facing. when I ask for status,
   give me a delta table with wall-clock time read from the system, not from memory.
   ```

2. **Have the lead create one builder per idea.** Tell the lead the idea. It writes a handoff (goal, context, files to read, worktree and branch, what counts as done) and then creates a new task with that handoff as the prompt. That new task is the builder. Never paste a prompt into a fresh session by hand.

   The lead can create the builder as a subagent inside its own session or as a separate desktop session. Both Codex and Claude Desktop can do either. If it is a desktop session, move it into a `Builders` group if that helps you keep track. It is only for you.
   - Codex: the lead creates a new task with the handoff as the prompt, in a separate worktree.
   - Claude Desktop: the lead calls `spawn_task` with the handoff as the prompt, which opens a new desktop session. A `/chip` skill that does this is a one-line shortcut. For work short enough to finish in one sitting it can use a subagent instead.

   Whatever else the lead puts in the builder prompt, make sure these two lines are in it. The first keeps builders out of each other's way. The second keeps them out of your inbox:
   ```
   worktree: .claude/worktrees/<slug>, branch <slug>, base <pinned SHA>
   report to: the session titled "<topic> lead"; never the operator
   ```

3. **Keep the chain.** Builders talk to the lead. The lead talks to you. If a builder messages you anyway, forward it to the lead.

4. **Ask the lead for status** whenever you want it.

5. **Land it on a remote branch, then archive the builder.** When a builder is done, or stuck past saving, the only thing that matters is that its work is on a remote branch or a draft PR. Once it is, archive the session. Merge the branch, fold it into another builder's work, or leave it. That is a later decision. A builder whose work only exists in its own worktree is one crash away from gone.

Sidebar groups such as `Leads` and `Builders` help you find things. They are for you. The agents do not need them, and telling a lead to only look at one group is too tight a constraint: a builder may be a desktop session or a subagent the lead spawned, and the lead should account for both. Prefer desktop sessions: you can see them, and they survive the session that opened them.

## Level 2: one lead and N sub-leads

Move here when the lead is answering builders faster than it can think. The lead stops managing builders and manages a few sub-leads instead. Each sub-lead owns one slice of the work and the builders inside it. Split by area of the codebase, by theme, by shared resource, or however the work already divides. Two or three is plenty to start.

1. **Reframe the lead.** Same session, new prompt:
   ```
   /loop 10m you are the lead for "<topic>". you manage the sub-leads; they manage the builders.
   I will try to keep sessions in the right desktop group, but you should understand work that
   a sub-lead spawned as a subagent and not strictly as a desktop session. keep the board at
   docs/experiments/BOARD.md from what the sub-leads report, rule on anything they escalate,
   and escalate to me only for blockers, big architecture decisions, or anything outward-facing.
   ```

2. **Open a sub-lead per slice.** A session on the repo, same place as the lead. Title it for its slice, for example `<topic> <slice>`. It writes handoffs, creates builders, and answers them:
   ```
   you are the sub-lead for "<slice>" under "<topic>". you report to the session titled
   "<topic> lead". when I or the lead give you an idea in your slice, write a handoff for it:
   goal, context, files to read, worktree and branch, what counts as done, and "report to
   <topic> <slice>, never the operator". create a builder from each handoff. prefer a separate
   desktop session, so the operator can see it and it survives you; use a subagent only for
   work short enough to finish in one sitting. either way, tell the lead the slug, the branch,
   and whether it is a session or a subagent. answer builders when they are stuck. if a builder
   needs something outside your slice, ask the lead. never build anything yourself.
   ```

3. **Give ideas to the sub-lead that owns the slice.** One message per idea. The report-to line in every builder prompt now names the sub-lead instead of the lead: `report to: the session titled "<topic> <slice>"`.

4. **Keep the chain**, one level deeper. Builders talk to their sub-lead. Sub-leads talk to the lead. The lead talks to you.

| role | count | where | manages |
|---|---|---|---|
| Lead | 1 | any checkout, no code edits | the sub-leads, the board, rulings |
| Sub-lead | a few | any checkout, no code edits | one slice: its handoffs, its builders, their questions |
| Builder | many | own worktree, session or subagent | one idea, one branch |

## Level 3: rules, tracking, and merging the sprawl

Move here when builders start colliding, when a dead builder takes its work with it, when the board stops matching reality, or when twenty finished branches are sitting there and nobody knows what to do with them. Each subsection stands alone. Add the one whose absence is costing you.

### Builder rules

Levels 1 and 2 only told builders where to work and who to report to. With many builders running, the failures are all the same: two builders in one worktree, a dependency install that links back into the root checkout and breaks everyone, a builder that dies with a day of work in an unpushed worktree, and a builder that hits the real database while testing. The block below is the standing set of rules that prevents those. Add it to every builder prompt, or put it in one file every prompt points at:

```
one worktree and one branch. never edit code in the root checkout.
install dependencies inside your worktree. never link to the root checkout's packages.
never `git worktree remove --force`.
commit and push WIP within 5 minutes of starting, then every 15 minutes.
never run the real app against real data. ladder: unit tests, typecheck and build,
headless browser over the built bundle, then ask for one operator run.
report to the lead or sub-lead that created you. never the operator.
```

### Exclusive resources

Some things only one session can touch at a time: a live app, a device, a database. Left alone, builders take turns clobbering it. The fix is a single owner and a file-based queue, so requests survive restarts and anyone can read who is waiting.

1. Make it a slice. Open a sub-lead whose only job is that resource, with the level 2 prompt plus:
   ```
   you are the only session that touches <the resource>. work on a scratch copy, never the
   original. before any write, check that every path the copy references points inside the copy.
   requests arrive as files in _queue/<slug>-<n>.md. write each outcome to
   _queue/<slug>-<n>-result/ and tell the requester and the lead.
   ```
2. Add to every builder prompt: `to use <the resource>, write a request to _queue/<slug>-<n>.md and wait for the result directory. never touch it directly.`
3. Have the owner keep `_queue/README.md`: one row per request with requester, time, state (waiting, running, done, failed), and result path.
4. Have the lead read that file every loop. A row unchanged for a full loop is a blocker the lead raises.

### Marking a builder done

"Done" in chat means nothing an hour later. You want a marker in the branch itself that says what the builder claims, and a way to tell whether it kept pushing afterward. The block below has each builder write a result file and pin the commit that holds it. Add to every builder prompt:

```
when done, write briefs/out/<slug>/RESULT.json: claims made, tests run, demo command, and
head_sha = the commit that holds this file. commit it. after that commit, change nothing.
```

The lead checks a finished builder with one command. It must name only RESULT.json:

```
git diff --stat <head_sha> <tip>
```

### Consolidating into themes

A sprawl of builders leaves many small branches that were never meant to merge one at a time. Group them into a few themes, each something you could demo as one story, and give each theme one consolidator that merges its members in order.

1. Have the lead group the done branches into a few themes, each something you could demo as one story. Ratify the grouping in one line.
2. Have the lead write the theme map on the board: for each theme, members in merge order with their head_sha, shared files to reconcile, flags that stay off.
3. When every member of a theme is done or abandoned, have the lead create a consolidator, in its own worktree on a theme branch, with the theme's map and:
   ```
   read every member's RESULT.json first. merge one member per commit, member tests green
   after each, whole suite at the end. record anything you could not reconcile in CONFLICTS.md.
   write DEMO.md that runs every member's demo in sequence. no PR. report to the lead.
   ```
4. Do not draw themes before the members are done. Early themes get redrawn.

### The board

The board is the lead's memory. Without it, every new builder re-asks questions the lead already answered, and you cannot tell what is running from what is done.

1. One untracked file per topic, `docs/experiments/<topic>-<yyyy-mm>.md`. The lead is its only writer.
2. Sections: idea families, owners, shared resources, queue, themes, decided.
3. Add to the lead prompt: `before answering any builder, check the decided section. never re-open a ruling already there.`

### The lead's loop

A lead that only reacts to messages misses the builder that went quiet and the branch nobody reported. This makes the lead sweep on a schedule as well. Add to the lead prompt:

```
every 20 minutes or on any message: read what the sub-leads report. run
git ls-remote --heads origin to catch branches they missed. update the board. before naming
any credential, path, or document in a handoff, verify it exists now. report a delta table
with wall-clock time read from the system.
```

### Your checklist

- Ratify direction and theme groupings in one line when asked.
- Do the human-only steps: logins, tokens, anything that needs your name or your credentials.
- Read the lead's delta tables.
- Forward stray builder messages to their sub-lead.
