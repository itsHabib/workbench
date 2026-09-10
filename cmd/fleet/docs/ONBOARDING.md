# Fleet: onboarding

For a person setting Fleet up on a machine, or an agent asked to. Thirty minutes to a working
fleet over one repository; read [OVERVIEW.md](OVERVIEW.md) first if the words are new.

## 1. Install the hook (operator, once per machine)

```sh
bash cmd/fleet/install.sh             # dry run: prints every change
bash cmd/fleet/install.sh --shadow    # optional: run beside the existing hook, write only telemetry
bash cmd/fleet/install.sh --apply     # build, back up harness configs, wire the hook, install lanes
```

State lives in `~/.fleet` (`FLEET_STATE` to move it). The installer edits harness configuration,
so a person runs it. Check: open any session and look for a `[fleet] session … · role ? · …`
line at start. `?` is correct for a directory with no role.

## 2. Decide the tree (five minutes of thinking, no commands)

- One overall lead. One lead per bucket of work (an epic, a repository, a team). One accountable
  lead per task, never two.
- Worker kinds you need: usually `author` (task to draft PR) and `verifier` (judges the exact
  head); `finisher` if drafts get carried through checks and reviews by someone else.
- One exclusive resource if the work has one (a device, a test bench, a deploy slot).
- Names: `supervisor:<name>` for leads, `<kind>:<repo>` for worker kinds.

## 3. Create the directories (ten minutes)

Everything beside the repository checkout, never inside it, never the checkout itself.

```sh
# leads: detached worktrees, then bound
git -C ~/dev/<repo> worktree add --detach ~/dev/<repo>-lead   main     # and -lead-a, -lead-b
org charter -role supervisor:<name>-a -tier T1 -scope github:<owner>/<repo> -supervisor human:<you> -supervisor supervisor:<name> -retire-when "<when>"
org charter -role supervisor:<name>   -tier T1 -scope role:supervisor:<name>-a -scope role:supervisor:<name>-b -scope github:<owner>/<repo> -supervisor human:<you> -retire-when "<when>"

# seats: pooled worktrees with names (pool before binding the leads, or unbind them while pooling)
fleet pool ~/dev/<repo> author 2 --tenant <t>
fleet pool ~/dev/<repo> verifier 1 --tenant <t>

# bind the lead directories
fleet role ~/dev/<repo>-lead   supervisor:<name>   --tenant <t>     # and the two bucket leads
```

Check with `fleet board`: every directory listed, every one `vacant`. Check one seat by opening
a headless session there and asking it to print its `[fleet]` lines; it should name its role and
seat without being told.

## 4. Write the run contract (one page, in the repository)

What the leads read for authority. Outcome; roles and who each may talk to; tasks with acceptance
and a stopping boundary; message ids; tick rules (one send per addressee per tick, any number of
local effects, end the turn); what nobody may do (merge, ready flip, revoke, other repositories);
the stop condition. The lead card and the `task-supervisor` skill supply the procedure; the
contract supplies the authority. A sample: `RUN-CONTRACT-v4.md` in `itsHabib/fleet-demo-sandbox`.

## 5. Choose how sessions get made

| shape | when | cost |
|---|---|---|
| desktop sessions in the lead directories on `/loop`, workers as chips into seats | you want to watch | highest; leads reconcile on a clock |
| headless ticks on a clock | unattended, simple | medium |
| headless on mail: one kickoff, then a delivery process starts a session for any address with unread mail and no live session | unattended, cheapest, sessions disposable | lowest; latency is the delivery interval |

For the third shape, `deliver.json` maps each address to a directory and a launch command
(prompt right after `-p`; `--allowedTools` is variadic and swallows a trailing prompt). Until the
watcher's own launcher lands, `e2e/mail-poll.sh` is the delivery process, or a desktop session on
`/loop 2m` doing the same job from an unroled directory.

## 6. Kick off and watch

The overall lead's first tick sends one `order` per child. Then:

```sh
fleet work            # rows and their observed state: dispatched · working · idle · late · done
fleet leases          # who holds which branch or resource
fleet mail --for <address>
fleet receipts
org log -role supervisor:<name>-a
```

Files in, sessions out, records left behind.

## 7. The rules that cost hours

- A repository root carries no role.
- Never `cd` into a roled directory that is not yours; launch from a subshell, use `git -C`.
- No idle desktop session in a roled directory; it reads as live and blocks delivery.
- One branch, one holder; only session end or an operator `fleet revoke` releases it.
- `fleet done <sha> --kind verify`; without `--kind` it demands every kind in `tier.json`.
- The hook's session id is not the desktop app's; the directory is the key both sides share.
- Slow commands need `FLEET_ALLOW_SLOW=<rule-slug>` — the slug of the rule the gate measured
  them under; the refusal prints the exact form, and no other token is accepted.

## 8. Prove it before trusting it

Run [e2e.md](e2e.md) once on the machine. Twelve minutes, a scorecard from records, and a
friction list with owners. Then run real work.
