# Headless Fleet

`fleet watch` owns mail delivery, assignment wakeups and recurring ticks. A wake starts one
provider turn; the next eligible wake resumes its recorded conversation. Desktop sessions
and native messaging remain supported. Fleet never infers completion from a process exit.

## Provider prerequisites

Install Node.js 22 or later. For Claude, install and authenticate the `claude` CLI, then install the official Agent SDK in a dedicated directory:

```sh
npm install --prefix "${FLEET_RUNTIME_HOME:-$HOME/.local/share/fleet/runtime}" \
  --no-audit --no-fund @anthropic-ai/claude-agent-sdk@0.3.183
```

For Codex, install and authenticate the `codex` CLI. Fleet uses `codex app-server` over local
stdio, with its initialize, thread/start, thread/resume, turn/start and turn/interrupt API.
Claude uses the SDK's streaming-input control channel, explicit `resume`, `interrupt()` and
`close()`. Token streaming is not required. The bridge is embedded in the Go binary; there is
no separate script to install and no second scheduler.

Both providers use their configured authentication and project settings. Claude loads user,
project and local settings, including the projected Fleet hooks: settings-file command hooks
do run in SDK-spawned sessions (verified on macOS with SDK 0.3.183; SessionStart,
UserPromptSubmit, Stop and SessionEnd wrote the session record and its events). A hook
command the platform cannot exec fails silently and leaves the session with no identity, so
the projected binary path must be runnable outside a POSIX shell; on Windows that means the
`.exe` extension, which `install.sh` now emits and `fleet role` now checks. Codex reads its
normal configuration. Set `CLAUDE_CONFIG_DIR` and `CODEX_HOME` when deliberately isolating
provider homes. Neither Fleet nor its installer copies credentials or chooses another account.

When `ANTHROPIC_BASE_URL` points at a custom endpoint (a gateway or proxy), the headless
session needs an explicit API key in the environment the watcher launches from; an
interactive `claude` login is not picked up, and the attempt records
`error: authentication_failed` with `provider_terminal: true`. Set the key for the watcher
process rather than in a shell profile: a global `ANTHROPIC_API_KEY` can override a credential
helper the interactive harness relies on. A gateway may also reject a dated model name where
the unversioned one (`claude-sonnet-5`) is accepted; that rejection reads like a permissions
error. `deliver.json`'s `model` is optional, and the provider default may be a dated name.

This integration reuses the supported mechanism already used by Ship's Claude runner; it does
not import Ship's policies or its runner (which does not support attach). Codex app-server
supplies explicit turn cancellation and identities beyond the batch CLI. Its API is versioned
with the installed CLI; verify a new version against the protocol and live tests before rollout.
See the official [Claude SDK](https://platform.claude.com/docs/en/agent-sdk/typescript) and
[Codex app-server](https://developers.openai.com/codex/app-server) references. Codex documents
app-server as experimental; this slice does not establish production qualification.

## Codex hook trust

Projecting `hooks.json` is not proof that Codex will execute it. Trust the exact Fleet hooks
in the target provider home before starting headless work. Through the installed app-server,
initialize with `experimentalApi: true`, then call `hooks/list` with the intended `cwds`.
Inspect each returned hook's source path, command, event and `currentHash`. For a hook you
intend to enable, `config/value/write` writes the object
`{enabled: true, trusted_hash: <that currentHash>}` at
`hooks.state.<quoted returned hook key>`, with `mergeStrategy: "replace"` and the intended
config file path. Do not generate a hash yourself or trust unrelated discovered hooks.
Alternatively use the Codex UI to enable them. Confirm an actual fresh session emits Fleet
SessionStart and tool events before dispatch. Changed hook commands require fresh trust.

## Configure a run

Bind directories with `fleet role` or `fleet pool`, then configure their actual mailbox
addresses in `$FLEET_STATE/deliver.json` (default `~/.fleet/deliver.json`):

```json
{
  "supervisor:project": {
    "cwd": "/absolute/path/project-lead",
    "provider": "claude",
    "permission_mode": "auto",
    "every": "2m",
    "prompt": "Read current Fleet work, mail and handoffs. Advance eligible work, leave a useful handoff, then end this turn when waiting. Stop this address when the run is complete."
  },
  "project-author-1": {
    "cwd": "/absolute/path/project-author-1",
    "provider": "codex"
  }
}
```

`provider` is required (`claude` or `codex`). `model` is optional; omission preserves the
provider default. Claude's optional `permission_mode` accepts `default`, `acceptEdits`,
`auto`, `plan` or `dontAsk`. Omission preserves its default. Fleet never supplies an operator
approval: an unhandled approval/input request is recorded and refused. Provider sandbox,
permissions, repository hooks, review and Gate still apply.

`every` requires a positive Go duration and a nonempty `prompt`. It schedules from the previous
launch time. Mail and assignments can wake sooner; a running attempt still excludes another
launch in the same directory. No default turn cap, lifetime or guessed budget is added. Keep
explicit run limits in the operator's run contract and provider configuration.

The old `cmd` array and `{{prompt}}` substitution are removed. Old entries are visibly invalid
and never launched. There is no compatibility launcher or automatic rewrite of installed homes.

## Start and continue

```sh
fleet watch --interval 10s
fleet dispatch task/example --as implementation --for supervisor:project \
  --slot project-author-1 --brief 'Implement the ticket through a tested draft PR.'
```

One Go watcher owns a state root. `watch --once` refuses while that watcher is running.
SessionStart can revive a stale watcher; `FLEET_WATCH=off` disables automatic revival for an
isolated test. Replacing a binary does not replace an already-running watcher.

An unread assignment with a brief wakes its seat without a second order. A worker awaiting a
peer writes a handoff, sends its request and ends its turn. Mail or recurrence supplies the next
turn in the same provider session. Hooks and provider observations retain the actual identity.
Continuation keeps the working directory and its dirty files; Fleet does not commit, stash,
reset or clean them. Branch changes remain subject to the existing assignment/lease checks.

A changed provider, tenant, address, repository, branch or assignment starts a fresh conversation.
Set `fresh: true` explicitly when each wake should start fresh or an unrecoverable session must
be replaced. Remove it to resume the newly recorded session on subsequent wakes. Unreadable or
mismatched retained provider state and failed resumes are errors, never silent fresh starts.
Once the bridge is known absent, matching provider-terminal or proven never-started evidence
releases the reservation even if a lost watcher could not collect its exit. A spawn failure
before a process exists preserves the originally requested session for the next eligible wake.
A live or uncertain bridge remains reserved. A provider child may still be running even when
the bridge exit was collected; without terminal or never-started evidence Fleet keeps the reservation.
An ended process alone does not prove all of its descendants have stopped.

## Observe and interrupt

```sh
fleet status --all --json
fleet watch status --json
fleet tail project-author-1 -n 20
fleet run-report --since 24h --json
fleet watch cancel project-author-1
```

`watch cancel` requests interruption of the exact current attempt through its unique control
file. It first verifies the retained process identity. The bridge calls the provider's interrupt
API, waits for a terminal response, then closes the transport. If no response arrives within ten
seconds, it closes the transport and reports the missing acknowledgment as failure. Inspect
status and exit evidence; a request is not proof of cancellation. Cancellation does not erase
mail, handoffs, assignments or working files and does not replay delivered work.

To stop future launches too, use `fleet stop address:project-author-1 "run complete"` before
cancelling. `fleet resume address:project-author-1` permits future starts. Removing a config entry
prevents future launches but leaves a current turn running; `watch cancel` still finds that
retained attempt independently of the current configuration. Stopping the watcher stops scheduling;
children continue and a replacement watcher may know their exit only as `gone_exit_unknown`.
Allow the watcher to collect all exits before stopping it when exact exit codes matter.

## Evidence contract

`watch/delivery/<cwd-hash>.json` points to the latest launch. Every attempt retains separate
`.meta.json`, `.log`, `.trace.jsonl`, `.state.json`, `.cancel` (only when requested) and `.exit.json` files.
`.trace.jsonl` contains native provider records; `.log` also includes normalized terminal results and transport diagnostics; `.exit.json` is the collected transport-process exit, not a task receipt.
No per-attempt file is reused by a later launch. `watch/observed.jsonl` records launch/exit and
scheduler observations. Private run output may contain task text and tool data; publish a
sanitized evidence summary rather than copying raw logs into a public PR.

The launch/meta record's `state_file` references this small provider summary:

```json
{
  "attempt": "/private/run/watch/delivery/unique-attempt",
  "provider": "codex",
  "provider_session": "actual-thread-id",
  "provider_turn": "actual-turn-id",
  "provider_state": "running",
  "last_provider_event": "item/started",
  "last_provider_event_at": 1789138635.02,
  "trace": "/private/run/watch/delivery/unique-attempt.trace.jsonl"
}
```

`provider_state` is `starting`, `running`, `blocked`, `interrupting`, `completed`, `interrupted`
or `failed`. `blocked` means the provider requested input/approval Fleet cannot supply; later
terminal evidence supersedes it. Times are Unix seconds, with fractions. Activity means an event
was observed, never inferred progress. Session/turn fields are absent until reported; Claude
reports session identity but has no matching app-server turn ID. `reason`, `error` and Codex
transport exit fields are included when known. `provider_started` records whether launch was
attempted and `provider_terminal` is true only after an actual provider terminal message.
A synthetic runtime error is not provider-terminal evidence. `provider_cleanup_pending` in
status identifies a collected bridge exit whose provider reservation remains held. Summary files contain no prompt or tool payloads.
Status joins a summary only when its attempt and provider match the current launch.

On macOS, a separate `provider_quiescent` proof can release a Codex attempt after an explicit
`initialize`, `thread/start` or `thread/resume` RPC rejection. Before dispatch, the bridge
persists `turn_may_have_been_sent`; it must be exactly false. The owned Go helper holds the
native process behind a pre-exec barrier, arms kernel fork/exec/exit observation, and writes
an attempt-bound `.state.json.process.json` proof only after exit. Release requires a complete
no-fork lifetime, observed native exec and exit, and no observer error. This is cleanup evidence,
not `provider_terminal` or successful work. The original resume target remains available for
retry; an explicit `fresh: true` can select a new session after safe release.

The installed official npm entrypoint is automatically resolved through its matching platform
package to the same native payload; native macOS installs run directly. No executable-path
setting is required. Unknown wrappers remain intact and cannot earn this proof. Any observed
fork, missing observation, possibly dispatched turn or ambiguous shutdown keeps the reservation.
This does not track descendants, force cleanup, or qualify Windows/Linux startup recovery.

The existing normalized terminal `result` shape is retained for `run-report`. Claude's SDK
reports cost and model turn count. Codex does not report matching cost/turn totals here, so they
remain unknown. A terminal success is not an independent receipt or proof of task completion.

## Validation

`go test ./cmd/fleet/...` exercises launch serialization and provider protocol fixtures (Node
required for the latter). Fixtures check fresh/resume identity, failed resume without fallback,
premature exit, cancellation and stale state. They establish mechanism behavior, not successful
provider authentication or useful agent work. The isolated real run evidence is recorded in
[provider-runtime-validation.md](provider-runtime-validation.md).
