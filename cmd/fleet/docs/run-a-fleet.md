# Run a fleet on a repository

Give each agent a directory, a clear result and a useful way to reach its peers. Fleet keeps
assignments, mail, handoffs, ownership and evidence. Agents decide how to do the work.

## Set up the workspace

1. Create a detached worktree for the lead and pool worker seats from the repository:
   ```sh
   git -C ~/dev/<repo> worktree add --detach ~/dev/<repo>-lead main
   fleet pool ~/dev/<repo> author 2 --tenant <tenant>
   fleet pool ~/dev/<repo> verifier 1 --tenant <tenant>
   fleet role ~/dev/<repo>-lead supervisor:<run> --tenant <tenant>
   ```
   Pool before binding the lead. A repository root carries no role: binding it projects
   permissions onto nested worktrees. Keep the main checkout clean and on `main`.
2. Write a short run brief: outcome, task acceptance, accountable lead, known peers, spending
   boundary, requested result (`draft`, `checks`, `reviews`, `ready`) and stop condition.
3. Start a fresh session in each kind of directory and verify the startup context identifies
   the actual role, seat and assignment. Use the installed build you intend to run.

Org is optional role prose, independent of Fleet. Register a card with
`org charter -role <name> -file ./role.md [-parent <name>]`, then read it with `org boot`.
Edit that file to add another repo or responsibility; no attach, claim or checkpoint is
needed. Remove old lifecycle commands and hooks during clean cutover; there is no
legacy fallback. See [Org's cutover inventory](../../org/README.md#clean-cutover).

## Choose execution

- **Desktop:** keep the working desktop loop and native messaging arrangement. Use actual
  native session addresses for native messages and Fleet addresses for Fleet mail.
- **Headless:** configure and run the existing Go watcher as described in
  [headless.md](headless.md). It supplies optional recurring lead ticks, assignment wakeups,
  mail delivery and child exit observation. No Bash/Python runtime poller or desktop session
  emulating one is required. Use one runtime owner per headless address.

## Assign and act

```sh
fleet dispatch <branch> --as implementation --for supervisor:<run>   --slot <repo>-author-1 --brief '<task and acceptance through a draft PR>'
```

The seat identifies the target repo; `--repo <owner/repo|checkout-path>` selects it explicitly.
Dispatch with a brief makes a configured headless seat eligible to start. No second order
message is needed. The caller stays in its own directory. Do not enter another roled directory
as its occupant; read its tree with `git -C`, or let its worker perform the work.

A lead advances eligible rows, answers questions and checks results. A worker continues until
its requested result or a real blocker. There is no one-action quota, universal one-hop rule,
or one-message-per-tick limit. Ask the relevant peer directly within the permitted tenant;
the assignment still names who is accountable.

When waiting for another agent, send the question or request, checkpoint what matters,
and end the turn. The Go watcher wakes you on mail or the next configured tick.
Continue other useful work first when available; waiting needs no shell sleep or mail-poll loop.
On waking, read the handoff and fresh mail, acknowledge handled messages, and continue.

## Checkpoint the work

Keep regular authored checkpoints using the existing `fleet handoff` mechanism:
after meaningful progress or a changed approach, before yielding or handing work over,
and at useful intervals during long work. A run brief can set the cadence in prose.
An idle tick with nothing new to record does not need another copy of the same checkpoint.

`fleet handoff` writes and replaces a checkpoint; it has no `--show` or `--list` read flag.
Read the context injected at SessionStart or inspect the JSON under `$FLEET_STATE/handoff/`
(worker branches) and `$FLEET_STATE/role-handoff/` (leads).

Record what changed or was learned, the evidence or file/PR pointers, any blocker,
and the next step. A pooled worker uses its branch handoff; a dedicated lead can use
`fleet handoff --role` for its cross-repository summary. This preserves authored context
for the next session; it does not introduce a checkpoint history or an Org lifecycle.

Runtime events record activity. Checkpoints explain the agent's understanding. Messages
address another agent: send one when someone needs to act, answer a question, or learn
about an important milestone. A routine checkpoint need not generate a message.

## Send messages

`fleet send` returns the message ID. Omit `--id` for a new message; retain the returned ID and
same payload for an intentional retry. Reply to `from_address`, with a new message ID and the
question/work reference in the body. Assignment startup supplies the dispatcher's actual
mailbox as `reply_to` by default, whether that dispatcher is a desktop seat or a headless role.
A role shared by several seats is not a unique mailbox: use the concrete address. See
[mail.md](mail.md) for storage and retry semantics.

## Recover and finish

Read `fleet work`, `fleet board`, the current branch and head, and the runtime's observations.
Resume the same assignment on its existing branch without deleting dirty files. Repurposing
a dirty seat for different work still requires preserving those files. `fleet unassign <seat>`
clears the placement and its matching dispatch rows together, retaining the tree and any live session/leases. Never take over another live writer or an exclusive resource.

`unoccupied` means a prior session left and nobody currently holds the branch. Read its
handoff and mail to understand why; it is not proof of abandonment. An expired due time
still reads `late`, and only a passing receipt establishes completion.

A verifier checks the exact head against acceptance and supplies the named receipt. The run
brief defines when independent verification is required; Fleet's receipt verb checks lane,
head and clean tree, not independence. A message or child exit saying done is insufficient.
Gate remains the separate merge-authority boundary.

For validation and measurement, use [e2e.md](e2e.md). The desktop run is a useful baseline:
compare completed work, coordination cost, idle assigned time and operator rescues, keeping
work acceptance and model/settings comparable. Record the actual binary and launch commands.
