# Fleet: overview

Fleet lets one person run several coding agents at once and always know who is doing what and
what is done, without any agent having to stop and report it. Knowing what is late, raised by
the system rather than noticed, is the open PR #301.

## The problem it solves

One agent in one terminal is easy. Three agents on three branches is where it goes wrong: two
of them edit the same branch, one says "done" and is not, the person becomes the message bus
between them, and nobody can tell afterwards who decided what. Every fix that asks agents to
check in, post status, or remember rules fails the same way: they don't, and the board lies.

## Four rules

1. **Location is identity.** A directory is bound to a role. Open a session in it and you are
   that role: the hook tells you so at start, the role's card and denies were projected into the
   directory when it was bound, and the hook refuses what the role may not do (writes on a held
   branch, a held resource, a stop flag, a slow command). No agent registers itself; the one
   binding is a line in `roles.map`. Nothing is remembered.
2. **Facts come from the hook.** Who is alive, who holds which branch or resource, what was
   written where, what a session said last: derived from harness events, never from an agent.
3. **The phone number is the role.** A message is a file addressed to a role or a seat. In the
   mail-driven shape a session for that role is started when a message arrives and ends when it
   has reported: no loops, no long-lived sessions. The launcher that does the starting is PR
   #301, in review; until it lands a small poller does that job (`e2e/mail-poll.sh`).
4. **Done is evidence.** A receipt at an exact commit, from a clean tree, by a session in a
   verifier seat, is what "done" means; that it is a *different* session from the implementer
   is a rule on the verifier card. A message saying "done" is not done.

## The shape

```
operator ─▶ overall lead ─┬─▶ lead A ─▶ worker seat A ─┐
                          └─▶ lead B ─▶ worker seat B ─┤─▶ one shared resource
                                        verifier seat ◀──┘
```

Leads decide and dispatch. Workers implement to a named boundary (draft, checks, reviews,
ready). A verifier judges the exact head. Escalation goes one hop up, never sideways, and only
the overall lead speaks to the operator; ordering between siblings is the overall lead's call.
Those three are rules on the lead and worker cards, held in every run so far, not refusals the
substrate makes.

## What it is made of

| piece | what it is |
|---|---|
| hook | one binary the harness calls on six events (start, prompt, before and after a tool, stop, end); writes the session record, leases, refusals |
| roles.map | directory → tenant, role, seat name; the one binding a session's identity comes from |
| seat | a pooled worktree with a name; its name is its mail address |
| row | a declared assignment: change, relationship, accountable role, due, seat |
| mail | one file per message under `mail/.v2/<tenant>/<role or seat>/<address>/<id>.json` (hashed names); send, read, ack; retry-safe by id |
| receipt | `receipts/<sha>.<kind>.json`; the only source of "done" |
| watcher | one per machine; folds the store into a board; delivering mail by starting sessions is PR #301 |
| lane | a kind of agent: a manifest (requires, produces, denies) plus a prose card |

Roles are data. Adding a kind of agent is a directory of two files, not a code change.

## What it has proved

Four live runs on 2026-09-09/10 (`itsHabib/fleet-demo-sandbox`, `docs/REHEARSAL-2026-09-09.md`,
scorecards under `runs/`): two leads under one, two workers contending for one resource, a
verifier, real refusals, real receipts. The last run: one kickoff session, then 18 sessions
created from 19 messages, both tasks verified in 12 minutes, zero messages to the operator, about
4x fewer output tokens than the same run with long-lived sessions (133k against 516k). Every
mistake the orchestration made was caught and became a numbered finding with an owner: a session
drifting into a seat was refused by the hook, a launcher starting four sessions for one role was
absorbed by Org's one-holder rule, a stale order was refused by the lead on the row's evidence.

## Where to go next

- Use it: [ONBOARDING.md](ONBOARDING.md), then [run-a-fleet.md](run-a-fleet.md).
- Prove a build: [e2e.md](e2e.md).
- Just the idea, no machinery: [MINIMUM.md](MINIMUM.md).
- The substrate itself: [../README.md](../README.md); the decisions:
  `docs/features/org-fleet-boundary/spec.md`.
