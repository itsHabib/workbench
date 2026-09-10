# Fleet: overview

Fleet lets one person run several coding agents at once and always know who is doing what,
what is stuck, and what is done, without any agent having to stop and report it.

## The problem it solves

One agent in one terminal is easy. Three agents on three branches is where it goes wrong: two
of them edit the same branch, one says "done" and is not, the person becomes the message bus
between them, and nobody can tell afterwards who decided what. Every fix that asks agents to
check in, post status, or remember rules fails the same way: they don't, and the board lies.

## Four rules

1. **Location is identity.** A directory is bound to a role. Open a session in it and you are
   that role: the hook tells you so at start, projects your instructions and your denies, and
   refuses anything that role may not do. Nothing is registered, nothing is remembered.
2. **Facts come from the hook.** Who is alive, who holds which branch or resource, what was
   written where, what a session said last: derived from harness events, never from an agent.
3. **The phone number is the role.** A message is a file addressed to a role or a seat. A
   session for that role is started when a message arrives and ends when it has reported.
   No loops, no long-lived sessions.
4. **Done is evidence.** A receipt at an exact commit, from a different session in a verifier
   seat, is what "done" means. A message saying "done" is not done.

## The shape

```
operator ─▶ overall lead ─┬─▶ lead A ─▶ worker seat A ─┐
                          └─▶ lead B ─▶ worker seat B ─┤─▶ one shared resource
                                        verifier seat ◀──┘
```

Leads decide and dispatch. Workers implement to a named boundary (draft, checks, reviews,
ready). A verifier judges the exact head. Escalation goes one hop up, never sideways, and only
the overall lead speaks to the operator. Ordering between siblings is the overall lead's call.

## What it is made of

| piece | what it is |
|---|---|
| hook | one binary the harness calls on every event; writes the session record, leases, refusals |
| roles.map | directory → tenant, role, seat name; the only registry |
| seat | a pooled worktree with a name; its name is its mail address |
| row | a declared assignment: change, relationship, accountable role, due, seat |
| mail | `mail/<address>/<id>.json`; send, read, ack; retry-safe by id |
| receipt | `receipts/<sha>.<kind>.json`; the only source of "done" |
| watcher | one per machine; folds the store into a board; delivers mail by starting sessions |
| lane | a kind of agent: a manifest (requires, produces, denies) plus a prose card |

Roles are data. Adding a kind of agent is a directory of two files, not a code change.

## What it has proved

Four live runs on 2026-09-09/10 (`itsHabib/fleet-demo-sandbox`, `docs/REHEARSAL-2026-09-09.md`):
two leads under one, two workers contending for one resource, a verifier, real refusals, real
receipts. The last run: one kickoff message, then 19 sessions created from 19 messages, both
tasks verified in 12 minutes, zero messages to the operator, a 4x drop in tokens against the
same run with long-lived sessions. Every mistake the orchestration made (a session drifting into
a seat, a launcher starting four sessions for one role, a stale order) was refused by the
substrate and became a numbered finding with an owner.

## Where to go next

- Use it: [ONBOARDING.md](ONBOARDING.md), then [run-a-fleet.md](run-a-fleet.md).
- Prove a build: [e2e.md](e2e.md).
- Just the idea, no machinery: [MINIMUM.md](MINIMUM.md).
- The substrate itself: [../README.md](../README.md); the decisions:
  `docs/features/org-fleet-boundary/spec.md`.
