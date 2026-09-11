# Headless Fleet: one Go runtime

`fleet watch` owns polling, recurring wakeups, mail delivery and process launches. Run the
current Go binary. The old Bash/Python mail poller is retired; desktop loops and native
messaging remain a supported desktop workflow, not a second headless launcher.

This pass keeps the existing command launcher while fixing scheduling and visibility.
`claude -p` below is the transitional batch interface, not the final session architecture.
The next runtime change covers Claude and Codex session continuation and structured lifecycle
observation; token-by-token text streaming is not a prerequisite. See the
[boundary decision](../../../docs/features/org-fleet-boundary/spec.md).

## Configure what runs

Bind each directory with `fleet role` or `fleet pool`, then put its actual mailbox address in
`$FLEET_STATE/deliver.json` (default `~/.fleet/deliver.json`):

```json
{
  "supervisor:project": {
    "cwd": "/absolute/path/project-lead",
    "cmd": ["claude", "-p", "{{prompt}}", "--output-format", "json"],
    "every": "2m",
    "prompt": "Read the run brief, current Fleet work and unread mail. Advance eligible work, answer questions, and leave a useful handoff when something changes. Stop when this tick has no more useful work."
  },
  "project-author-1": {
    "cwd": "/absolute/path/project-author-1",
    "cmd": ["claude", "-p", "{{prompt}}", "--output-format", "json"]
  }
}
```

Use the configured harness and its actual flags on this machine. `cmd` is an argument array,
not a shell expression; `{{prompt}}` is replaced in each argument. The runtime adds no turn
limit. Retain the operator's explicit spending and stop limits in the run and harness
configuration; do not introduce guessed author/lead turn profiles. An explicit low turn cap
can still kill a configured worker, so inspect the actual command before launching real work.

`every` is optional. It requires a positive Go duration and a nonempty `prompt`. A lead with
`every` gets a tick when the interval since its last launch has passed, even without mail.
Removing `every` removes future periodic wakeups; removing an entry prevents future launches
for that address. A running worker is not killed by changing the config. Keep recurring
entries only while the run is active; the brief's stop condition tells the lead when to report
completion, and the operator removes or disables its recurrence. No hidden 45-minute lifetime.

## Run it

```sh
fleet watch --interval 10s
```

One watcher owns the runtime. SessionStart can revive a stale watcher automatically; inspect
`$FLEET_STATE/watch/heartbeat.json` before starting another foreground watcher. An existing
watcher keeps the binary it started with: after building a replacement, stop the old watcher
and run the new binary. First stop any old script poller or desktop session acting as a
headless launcher so two different runtimes do not compete.

The watcher responds to interrupt/termination and records `watcher-stopped`. Set
`FLEET_WATCH=off` in the launching harness environment when automatic revival is unwanted.
Stopping the watcher stops new launches; existing workers retain their files and continue.
Use the existing work stop/lease controls and the harness's cancellation mechanism when the
intent is to stop the work itself.

## Assignment starts work

```sh
fleet dispatch task/example --as implementation --for supervisor:project   --slot project-author-1 --brief 'Implement the ticket through a tested draft PR.'
```

A matching configured seat with a current, unread assignment and a brief becomes eligible on
the next watcher tick. No second `fleet send --kind order` is necessary. The seat determines
the target repository; `--repo owner/repo` or `--repo /path/to/checkout` selects it explicitly.
The caller stays in its own directory and retains its identity.

The startup hook presents the current assignment. Agents continue authorized work until the
requested result or a real blocker. A failed or interrupted worker can be resumed on the same
branch with its dirty files intact; assigning a different branch still requires protecting
those files. `fleet unassign <seat>` removes its placement and matching dispatch rows together,
retains working files and leaves any live session and its leases intact. It removes the
declaration, not execution ownership.

## Delivery and recovery

Mail, new assignments, and recurring lead ticks share one serialized launch path. One launch
carries the pending mail and assignment together. The runtime records a child before its
first harness hook, so another message cannot start a second worker in that gap. Existing
live sessions still suppress launches, including native desktop sessions actively working in
the directory. Configure each headless address for one directory.

Launches and output live under `watch/delivery/`; the directory's hashed `.json` file records
the latest launch and each attempt has an `.exit.json` result. `watch/observed.jsonl` records
attempts, starts, failed starts, child exits and watcher shutdown. A nonzero exit is observable;
it does not cause an automatic replay or an automatic commit of the worker's files. Inspect
the retained assignment, mail and working tree, then explicitly resume the same work.

The persistent watcher reaps the children it starts and records their exit codes. A child can
outlive a stopped watcher or a `watch --once` invocation. After that, a later watcher checks the
recorded process; the exact exit code is unknown if the original parent did not collect it.
A PID reused by an unrelated process can conservatively delay a launch. A crash around process
start or unreadable launch state is ambiguous and requires inspection rather than guessing
that it is safe to duplicate work. This is process observation, not exactly-once execution.

A failed process start returns its mail reservations for retry. Delivered mail is retained and
never automatically replayed; acknowledgment records reading, not completion. Receipts and
Git/CI evidence establish the requested result. A completed process is not a completed task.

`fleet watch --once` is useful for a diagnostic fold when no watcher is running; it refuses
while the persistent watcher owns the board. Use `watch status` to inspect a live watcher.
Use the persistent watcher for normal
headless work and exit collection. Use `fleet report` and the existing observation logs to
score runs; offline analysis scripts may summarize them without owning scheduling or state.

## See what is happening between ticks

```sh
fleet watch status
fleet watch status --json
fleet tail project-author-1 -n 20
fleet tail project-author-1 -f
```

`watch status` is read-only: it neither folds a scheduler tick nor launches work. It shows the watcher's
last heartbeat separately from each configured worker's process state, launch time, latest
observed hook/tool/session, output path and modification time, and collected exit result.
Configuration/binding problems and explicit stops are visible. `watch/observed.jsonl` is the
runtime trace; each row points to the worker output and launch/exit files for investigation.

A missing hook is shown as missing. A PID without an exit result is not called successful.
Output timestamps show writes, not semantic progress. With batch JSON output, model text may
remain buffered until exit; hook activity can still arrive while it runs. This view does not
yet identify provider-specific waiting/approval states; that needs the later session interface.
`fleet tail <seat|role> [-n N] [-f]` reads recent visible assistant text and tool calls from
the transcript path recorded by hooks, falling back to that launch's output. It follows file
replacement and newly observed sessions in Go. It skips reasoning blocks, retains partial
JSONL records for the next read, and bounds each read to 1 MiB; exceptionally large records
report the raw source for inspection. Result records show provider reason/cost/turns only when
those fields are present. No transcript is guessed when the harness supplied no path.

Use `--json` for an operator UI instead of teaching a UI to infer worker state from tick logs.

## September 10 issue dispositions

The reported failures were in the headless run. These changes address
[workbench #304](https://github.com/itsHabib/workbench/issues/304) ,
[workbench #306](https://github.com/itsHabib/workbench/issues/306), and
[workbench #308](https://github.com/itsHabib/workbench/issues/308) without replacing the working
desktop loop/native-message setup.

| Reported friction | Treatment |
|---|---|
| Worker killed at 60 turns | No cap added by Fleet; remove the blanket cap from supported examples and use explicit operator budget settings. Existing installed commands must be updated deliberately. |
| Poller expires after 45 minutes | Retire the Bash/Python runtime poller; the Go watcher runs until stopped and records shutdown. |
| Dispatch leaves a worker asleep | The Go watcher reads the current seat assignment directly; a brief is enough to make a configured seat eligible. |
| Lead never gets another turn | Optional `every` and `prompt` provide recurring ticks in the same Go runtime. |
| Failed worker leaves silence and dirty files | Record process exits and retain the tree. Resume the same branch; do not automatically commit or discard untracked files. |
| Scratch file blocks continuation | Same-branch continuation is allowed when the seat is free or its previous process is known to have ended. Different work still requires preserving the dirty tree. |
| Cross-repo lead must change cwd | Select the target through the seat or `--repo`; caller identity stays where it started. |
| Desktop lead has a different address shape | Assignment `reply_to` defaults to the actual caller mailbox; replies use `from_address`. Distinct seats keep distinct addresses; Fleet does not guess between them. |
| Reused reply ID conflicts | New messages can omit the ID. Reuse a returned ID only for the same-message retry; a reply gets its own ID. No mailbox migration or weaker overwrite protection. |
| Unassign leaves another row | Clear the placement and its matching dispatch rows in one operation, retaining files and leases. |
| Org bootstrap and immutable scope interrupt new Fleet work | Org now registers editable prose cards with an optional parent. The normal CLI/MCP has three operations, and no work or chain ceremony. Old lifecycle callers are removed during cutover. |
| Too much coordination procedure | Remove universal one-action, upward-only and message-quota instructions; handoffs contain useful conclusions. |
| Cannot see headless activity between ticks | Read `fleet watch status [--json]` for worker/process/hook/exit evidence and output paths, separately from watcher health. |
| Need run metrics | Use existing `fleet report`, runtime observations and offline scorecard analysis. A new reporting service is unnecessary. |

Old Baton journals are inert historical files; the new runtime does not read them.
Registering or editing a card does not rewrite those records, migrate held work, or alter
resource/merge authority. Deployment and retiring installed legacy callers remain explicit
follow-through; the runtime provider replacement is the separate next step described above.

Tests use real child processes, Git worktrees and scratch stores. They qualify the runtime
mechanics and refusal boundaries; a repeated real-task headless run is still needed to measure
PR throughput, coordination spend and operator rescues against the desktop baseline.

For #308, this pass supplies live process/hook/exit inspection and transcript tailing.
Provider waiting states, uniform Claude/Codex terminal semantics, a unified all-seat/work/mail
table and a new operator push channel remain open. Existing `FLEET_NOTIFY` handles configured
watcher notifications; this patch does not configure a new external destination. Existing
`fleet report` and offline metrics remain the reporting tools.

## Yield and stop

An agent waiting for a peer records its handoff and ends the turn. Mail or the configured
recurrence wakes a fresh session with the retained context. Keep shell polling out of role
cards: the Go watcher already owns that wait.

To finish a run, remove its delivery entries first, let active sessions settle while the
watcher captures their exits, then stop the watcher. Removing an entry prevents new launches
and does not kill its active process. Stopping the watcher before children exit loses OS exit
observation; `gone_exit_unknown` is honest even if a later model result says success.

## Stop an address

At the run's stop condition, a lead can run `fleet stop address:supervisor:<run> "run complete"`.
This pauses new mail, assignment and recurring launches for that address, even in a detached
lead worktree. `fleet resume address:supervisor:<run>` clears the stop. Status reports further
starts paused. Running sessions continue, so the watcher can capture their exits.

A live idle session still occupies its launch directory; end it before allowing a replacement.
Each watcher launch records process start identity as well as PID. A recycled PID is a departed
launch, and uninspectable identity is reported as unknown for inspection. Neither is silently
reported as healthy work. The watcher adds no default turn budget or runtime lifetime; keep
explicit spending limits in the harness/run brief and stop the address when the outcome is met.
