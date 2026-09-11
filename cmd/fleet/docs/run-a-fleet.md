# Run a fleet on any repository

An agent guide. Read it when you are asked to stand up leads and workers over a repository, or to
act as one of them. It assumes the installed hook (`~/.fleet/bin/fleet hook claude`, or the Codex
variant) and a state root at `$FLEET_STATE` (default `~/.fleet`) with Org state at `$ORG_STATE`.
It says what to create, in which order, and what each session will see; it does not restate the
verbs (run `fleet` for those) or the design (`../README.md`).

Evidence for everything here: four live runs recorded in `itsHabib/fleet-demo-sandbox`,
`docs/REHEARSAL-2026-09-09.md`, and the merged decisions in
`docs/features/org-fleet-boundary/spec.md`.

## The five things a fleet is made of

| thing | what it is | created by |
|---|---|---|
| a **role** | a name like `supervisor:<name>` (a lead) or `<kind>:<repo>` (a worker kind) | `org charter` for leads; lanes under `~/.fleet/lanes/<kind>/` for worker kinds |
| a **roled directory** | a directory bound to one role in `roles.map`; a session opened there *is* that role | `fleet role <dir> <role> --tenant <t>` |
| a **seat** | a roled directory that is also a pooled worktree with a name; the name is its mail address | `fleet pool <checkout> <kind> <n> --tenant <t>` |
| a **row** | a declared assignment: change, relationship, accountable role, due, seat | `fleet dispatch <branch> --as <kind> --for <lead> --slot <seat> --brief ... --reply-to <sid>` |
| a **message** | a file addressed to a role or seat; the only way roles talk | `fleet send <address> --id <id> --kind <kind> --subject ... --body ...` |

Rules that follow from the code, not from politeness:

- **A repository root carries no role.** Roled directories are seats and lead directories made for
  the purpose. Roling a root projects that role's denies onto every worktree under it.
- **Never `cd` into another roled directory.** The session that does becomes that directory's
  occupant and leases its branch. Launch workers from a subshell (`(cd <seat> && ...)`) or use
  `git -C <seat>`.
- **A branch has one live holder.** The hook leases it to the first writer; a rival is refused
  with the holder's name. Session end releases it; the next guarded writer takes over a
  known-dead holder; unreadable evidence is not death; resources need an explicit drop or a
  checked takeover; `fleet revoke` is the operator's.
- **A seat's address is its seat name; a lead's address is its role.** Sending to a pooled kind
  (`author:<repo>`) refuses and lists the seats.
- **Done requires a passing receipt at the exact head.** The verb checks the producing lane, the
  roled worktree, HEAD and a clean tree. The run contract additionally requires a verifier session
  different from the implementer, and the lead checks that. `fleet receipt <sha> verify pass
  "<observable>"`, then `fleet done <sha> --kind verify`.

## Stand it up, in order

Given a repository checkout `~/dev/<repo>` (never inside another repo), a tenant `<t>`, and the
lead names you want:

1. **Charter the leads** (Org). One overall lead scoping its children by `role:` and the repo;
   each bucket lead scoping the repo:
   ```sh
   org charter -role supervisor:<name>-a -tier T1 -scope github:<owner>/<repo> -supervisor human:<you> -supervisor supervisor:<name> -retire-when "<condition>"
   org charter -role supervisor:<name>   -tier T1 -scope role:supervisor:<name>-a -scope role:supervisor:<name>-b -scope github:<owner>/<repo> -supervisor human:<you> -retire-when "<condition>"
   ```
2. **Create the lead worktrees, unbound, then pool the seats.**
   ```sh
   git -C ~/dev/<repo> worktree add --detach ~/dev/<repo>-lead   main    # and -lead-a, -lead-b
   fleet pool ~/dev/<repo> author 2 --tenant <t>
   fleet pool ~/dev/<repo> verifier 1 --tenant <t>
   ```
   Each seat gets `CLAUDE.local.md` (the card), `.claude/settings.local.json` (denies and the
   write hook), a Codex config, and a `roles.map` line with its name. Pool first: `fleet pool`
   refuses when worktrees of the same repo already carry other labels, and there is no unbind verb.
3. **Bind the lead directories only after pooling succeeds.**
   ```sh
   fleet role ~/dev/<repo>-lead supervisor:<name> --tenant <t>     # repeat for -lead-a, -lead-b
   ```
   Then check bindings and seat names with `fleet board`.
4. **Check what a fresh session sees** before trusting anything: open a headless session in a
   seat and ask it to print its `[fleet]` lines. It should name its session, role, branch and
   seat, with no prompt text about roles.
5. **Write the run contract**, one page in the repository the leads can read: outcome, roles and
   their contacts, tasks with acceptance and boundary (`draft`, `checks`, `reviews`, `ready`),
   message ids, tick rules, authority and the stop condition. The lead card and the
   `task-supervisor` skill supply the procedure; the contract supplies the authority.
6. **Decide how sessions are made.** Three shapes have run:
   - *Desktop chips* with cwd set to a roled directory, leads on `/loop`, workers as chips.
     Native messaging works between desktop sessions; the hook's session id is not the desktop's,
     so agents find each other by directory.
   - *Headless on a clock*: `claude -p "/task-supervisor"` from each lead directory on a cadence.
   - *Headless on mail*: one kickoff tick, then a delivery process starts a session for an address
     when it has unread mail and no live session — `fleet watch` does this itself, one launch per
     address per fold, and sends lateness as mail on the same fold (see `e2e.md`). This is the cheapest and the one that keeps sessions disposable.
7. **Kick off**: the overall lead's first tick sends one `order` per child. From then on, files in,
   sessions out.

## Acting as a role

- **A lead tick**: read your mail (`fleet mail --unacked`), your chain, your rows (`fleet work
  --for <you>`), the PRs; act on every eligible effect (dispatch, assignment, verify row, an
  answer); one send per addressee; ack what you handled; checkpoint, yield, release; end the turn.
  Escalate one hop up, never sideways, never to the operator unless you are the overall lead.
- **A worker step**: read your mail and your assignment line; do the next step the contract
  allows; report or ask by mail with the contract's id; end the turn. Never wait for a reply in
  a session. If a take is refused, report the refusal verbatim; never retry or `--takeover`.
- **A verifier**: confirm exact head and clean tree, confirm your session differs from the
  implementer's, check the acceptance, emit the receipt, comment the PR, report by mail.

## Reading the fleet

`fleet board` (who is where), `fleet work` (rows and their observed state), `fleet leases`,
`fleet receipts`, `fleet mail --for <address>`, `org status` and `org log -role <lead>` for the
leads' records, `~/.fleet/watch/observed.jsonl` for what the watcher saw change. A message
saying "done" is not done; a row reads `done` only from a receipt.

## What to record

Friction goes in a record with reproducer, expected, actual and owner, never in chat. The
rehearsal record's friction list is the template. Before the next run, reset rows and seats
from a subshell in the checkout and refresh every worktree to `main`.
