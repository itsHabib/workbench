# Fleet: onboarding

For a person setting Fleet up on a machine, or an agent asked to. Set up one repository, then
rehearse on this machine and record elapsed time and friction; read [OVERVIEW.md](OVERVIEW.md)
first if the words are new. Everything measured so far was measured on a Mac with Claude.

## 1. Install and bind roles

Follow [install.md](install.md) for the public checkout, prerequisites, complete
fresh-install commands and a disposable packaging check. The installer builds Fleet
and installs bundled cards; `fleet role` projects both harnesses' instructions and
hooks. A private skill checkout and old Python hook installation are not required.

## 2. Decide the tree (five minutes of thinking, no commands)

- One overall lead. One lead per bucket of work (an epic, a repository, a team). One accountable
  lead per task, never two.
- Worker kinds: bundled `author` (task owner), `verifier` (exact-head checks) and
  `supervisor` (coordination). Add custom lane manifests/cards only when needed.
- One exclusive resource if the work has one (a device, a test bench, a deploy slot).
- Names: `supervisor:<name>` for leads, `<kind>:<repo>` for worker kinds.

## 3. Create the directories (ten minutes)

Everything beside the repository checkout, never inside it, never the checkout itself.

```sh
# leads: detached worktrees, then bound
git -C ~/dev/<repo> worktree add --detach ~/dev/<repo>-lead   main     # and -lead-a, -lead-b

# seats: pooled worktrees with names (pool before binding the leads; there is no unbind verb, so
# if leads are already bound, remove their roles.map lines while pooling and re-run fleet role)
fleet pool ~/dev/<repo> author 2 --tenant <t>
fleet pool ~/dev/<repo> verifier 1 --tenant <t>

# bind the lead directories
fleet role ~/dev/<repo>-lead   supervisor:<name>   --tenant <t>     # and the two bucket leads
```

Check with `fleet board`: every directory listed, every one `vacant`. Check one seat by opening
a headless session there and asking it to print its `[fleet]` lines; it should name its role and
seat without being told.

## 4. Write the run contract (one page, in the repository)

Record the outcome, accountable lead and useful contacts, task acceptance, requested result,
spending boundary and stop condition. Agents can communicate directly and continue eligible
work; there is no message quota. New Fleet work uses its assignment and handoff without an
Org charter or claim sequence. Existing Org-held work and terms keep their original owner
until an explicit migration. The run brief records the authorized scope. For example:

> Outcome: repair the parser failure and open a draft PR. Lead: supervisor:demo.
> Owner: repo-author-1; checker: repo-verifier-1. Acceptance: reproduce the failure,
> add a focused regression check, and pass repository checks at the reviewed head.
> Use only the configured model budget. Stop at the reviewed draft or a concrete blocker;
> merging and deployment require separate authorization.

## 5. Choose how sessions get made

Desktop loops and native messaging are the working desktop baseline. Headless execution uses
one Go `fleet watch`: [headless.md](headless.md) covers configuration, optional recurring lead
ticks, assignment-triggered starts, mail and exit observation. There is no supported shell
poller. Historical runs do not establish a cost ranking between these choices; compare actual
completed work and coordination cost with the same acceptance.

## 6. Kick off and watch

The lead dispatches work with a brief; configured seats start on the next watcher fold. Then:

```sh
fleet work            # rows and their observed state: dispatched · working · idle · late · unoccupied · dead · failed · undeclared · remote · done
fleet leases          # who holds which branch or resource
fleet mail --for <address>   # only inside a live roled session; from an operator shell use board/work/leases/receipts
fleet receipts
fleet report
```

Files in, sessions out, records left behind.

## 7. Before kickoff

Check the operating rules in [run-a-fleet.md](run-a-fleet.md): keep the repository root unbound,
stay in your own directory, inspect holders, clear idle sessions from delivery addresses. Three
that are only here:

- `fleet done <sha> --kind verify`; without `--kind`, done expects every receipt kind the installed
  lane manifests produce.
- The hook's session id is not the desktop app's; the directory is the key both sides share.
- A command matching a rule in `~/.fleet/expensive.json` (hand-written thresholds) needs the
  `FLEET_ALLOW_SLOW=<rule-slug>` prefix — the slug of the rule the gate matched it under, and no
  other token is accepted; the refusal prints the exact form, and the seat's allow list must
  accept it. The override covers that one command only.

## 8. Prove it before trusting it

Run [e2e.md](e2e.md) once on the machine: a scorecard from records and a friction list with
owners. Then run real work.
