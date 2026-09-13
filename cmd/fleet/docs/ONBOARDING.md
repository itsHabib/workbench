# Fleet: onboarding

For a person setting Fleet up on a machine, or an agent asked to. Set up one repository, then
rehearse on this machine and record elapsed time and friction; read [OVERVIEW.md](OVERVIEW.md)
first if the words are new, or use [Fleet 101](fleet-101.md) for one task. Bounded macOS
workflows have run with both providers; see [the dated evidence](provider-runtime-validation.md).
Windows and larger scenarios require their own qualification.

## 1. Install and bind roles

Follow [install.md](install.md) for the public checkout, prerequisites, complete
fresh-install commands and a disposable packaging check. The installer builds Fleet
and installs example cards; `fleet role` projects both harnesses' instructions and
hooks. A private skill checkout and old Python hook installation are not required.

## 2. Choose responsibilities

Start with one accountable lead and one task owner. Add an independent verifier when
acceptance calls for one; add another worker, lead or exclusive resource only when the
work requires it. Example role cards are starting points you can edit, not a prescribed tree.

## 3. Create the directories

Use the exact setup in [install.md](install.md#configure-one-repository): pool seats before
binding the lead, keep the main checkout unbound, and use separate worktrees. Then confirm
startup context in a fresh supported session. The setup creates configuration; observed
hook events establish whether the harness actually loaded it.

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
