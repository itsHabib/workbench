# Fleet: onboarding

For a person setting Fleet up on a machine, or an agent asked to. Set up one repository, then
rehearse on this machine and record elapsed time and friction; read [OVERVIEW.md](OVERVIEW.md)
first if the words are new. Everything measured so far was measured on a Mac with Claude.

## 1. Install the hook (operator, once per machine)

Mac or Linux. (Windows harness configs with escaped backslash paths are unsafe for `--apply`,
see [hook-inspection.md](hook-inspection.md); register the hooks by hand there and skip the
installer.) Prerequisites: Go, Git, Bash and Python 3; a cc-skills checkout for the lane cards
(`LANES_SRC` pointing at its lanes directory); the built `fleet` binary on `PATH`.

```sh
bash cmd/fleet/install.sh             # dry run: prints every change
bash cmd/fleet/install.sh --shadow    # optional: run beside the existing hook, write only telemetry
bash cmd/fleet/install.sh --apply     # build, back up harness configs, swap the hook lines it finds, install lanes
```

`--apply` is an upgrade path: it replaces hook registrations that already exist in the harness
configs and skips missing files and a missing lane source; on a fresh machine it prints
`installed` and wires nothing. Bootstrap by hand first: in `~/.claude/settings.json`, under
`hooks`, register the same command for each of the six events `SessionStart`,
`UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, `SessionEnd`:

```json
"SessionStart": [{"hooks": [{"type": "command", "command": "$HOME/.fleet/bin/fleet hook claude", "timeout": 5}]}]
```

(`PreToolUse` and `PostToolUse` take a `"matcher"` covering `Bash` and the file-writing tools, for
example `"Bash|Edit|Write"`; copy the exact matchers from an installed machine's settings when you have one.)
Codex registrations are user-level: `fleet role` writes the six events to `$CODEX_HOME/hooks.json` the first time it binds a directory, so they run for every Codex session on the machine; only the card and rules are per directory. Then
`fleet inspect-hooks --config ~/.claude/settings.json` must list all six, then run `--apply` for
the lanes and backups, then open a fresh session and look for the `[fleet]` line. Stop if the
installer says a config or the lane source was skipped. State lives in `~/.fleet` (`FLEET_STATE`
to move it). The installer edits harness configuration, so a person runs it. Check: open any session and look for a `[fleet] session … · role ? · …`
line at start. `?` is correct for a directory with no role.

## 2. Decide the tree (five minutes of thinking, no commands)

- One overall lead. One lead per bucket of work (an epic, a repository, a team). One accountable
  lead per task, never two.
- Worker kinds you need: usually `author` (task to draft PR) and `verifier` (judges the exact
  head); `finisher` if drafts get carried through checks and reviews by someone else. Lanes are
  installed from cc-skills; check `~/.fleet/lanes/` has the kinds you name (a fresh install may
  lack `verifier`; copy it from the cc-skills lanes directory).
- One exclusive resource if the work has one (a device, a test bench, a deploy slot).
- Names: `supervisor:<name>` for leads, `<kind>:<repo>` for worker kinds.

## 3. Create the directories (ten minutes)

Everything beside the repository checkout, never inside it, never the checkout itself.

```sh
# leads: detached worktrees, then bound
git -C ~/dev/<repo> worktree add --detach ~/dev/<repo>-lead   main     # and -lead-a, -lead-b
org charter -role supervisor:<name>-a -tier T1 -scope github:<owner>/<repo> -supervisor human:<you> -supervisor supervisor:<name> -retire-when "<when>"
org charter -role supervisor:<name>   -tier T1 -scope role:supervisor:<name>-a -scope role:supervisor:<name>-b -scope github:<owner>/<repo> -supervisor human:<you> -retire-when "<when>"

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

What the leads read for authority. Outcome; roles and who each may talk to; tasks with acceptance
and a stopping boundary; message ids; tick rules (one send per addressee per tick, any number of
local effects, end the turn); what nobody may do (merge, ready flip, revoke, other repositories);
the stop condition. The lead card and the `task-supervisor` skill supply the procedure; the
contract supplies the authority. A sample: `docs/RUN-CONTRACT-v4.md` in `itsHabib/fleet-demo-sandbox`.

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
fleet work            # rows and their observed state: dispatched · working · idle · late · abandoned · dead · failed · undeclared · remote · done
fleet leases          # who holds which branch or resource
fleet mail --for <address>   # only inside a live roled session; from an operator shell use board/work/leases/receipts
fleet receipts
org log -role supervisor:<name>-a
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
