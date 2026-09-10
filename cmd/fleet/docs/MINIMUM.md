# The minimum: more than one agent, without the machinery

Fleet is a substrate: hooks, leases, receipts, mail, a watcher. Strip all of that away and ask
what actually made several agents work together in the runs, and it is five habits and two
files. You can adopt them today with nothing but a harness, a repository and prose.

## What the machinery was protecting

Every piece of Fleet exists because one of these went wrong without it:

| failure | what stops it |
|---|---|
| two agents write the same branch | one branch per agent, decided before anyone starts |
| "done" that is not done | done means a named check by a different agent at a named commit |
| the person becomes the message bus | questions go to a named parent, in a fixed shape, with an id |
| nobody knows who decided what | every decision written where the next agent reads it |
| agents forget the rules | the rules live where the agent starts, not in its memory |

Fleet enforces the first two with a hook and gives the next two a verb; the last is a card the
agent reads at start. Without the hook the first two are conventions. Conventions with a
written shape hold surprisingly well, because the agents read the shape at start.

## The five habits

1. **One directory per agent, one branch per directory.** A lead gets a directory; each worker
   gets a worktree with its own branch; nobody shares. Put a short `CLAUDE.local.md` (or the
   Codex equivalent) in each: "You are the worker for task X on branch Y. Your lead is Z."
   That file is the whole identity. An agent that opens there knows who it is.
2. **One page of contract, in the repository.** Outcome, tasks with acceptance, who talks to
   whom, what nobody may do, when to stop. Leads read it before acting. Authority comes from
   the page, not from the conversation.
3. **A message shape with an id.** Every question or report is one paragraph: task, commit,
   kind (question, answer, report, escalation, order), id, body. Replies reuse the id. Put them where
   the reader will look: a PR comment, a file in a `mail/` directory, a chat message. The id is
   what lets anyone reconstruct the conversation later.
4. **One hop up.** A worker asks its lead. A lead asks the overall lead. Only the overall lead
   asks the person. Siblings never settle ordering between themselves. This single rule is what
   keeps the person out of the loop and keeps decisions attributable.
5. **Done is a named check by someone else.** Before "done" means anything, a different agent
   checks the exact commit against the acceptance and writes one line: commit, pass or fail,
   what it observed. Draft is the default boundary; going past it needs explicit permission.

## The two files

- **`CLAUDE.local.md` per directory** (identity and the rules above in a few lines).
- **`docs/RUN-CONTRACT.md` in the repository** (authority and the stop condition in one page; the
  sandbox keeps versioned copies, `docs/RUN-CONTRACT-v4.md` is the latest).

## The loop

A lead session, on a `/loop` or by hand, does one tick: read the contract, the messages
addressed to it, and the state of its tasks; act on everything eligible (start a worker, answer
a question, ask a verifier); send at most one message per addressee; write one line of what it
concluded; stop. Workers do one step and stop. Nothing waits inside a session; waiting is a
message that has not been answered yet.

## What you give up without Fleet

Enforcement. A convention can be broken by a session that forgets; the hook cannot, though it
only refuses what it can see (a `cd` into another seat slipped past it twice until PR #300). Liveness
and lateness are things you notice, not things the system tells you. Two seats can collide if a
person makes a mistake in the setup. Those are the reasons the substrate exists, and the order
to add its pieces if the habits hold and the team grows: leases first (one writer per branch),
receipts second (done as evidence), mail third (roles as addresses), the watcher last (sessions
started by messages, lateness raised by the system).

## The sentence

Give each agent a place that says who it is, give the team a page that says what is allowed,
make every message carry an id and go one hop up, and let "done" be another agent's written
observation of a commit. That is most of the value; the rest is making it impossible to forget.
