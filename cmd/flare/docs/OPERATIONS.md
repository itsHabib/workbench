# flare — operations runbook

How to run `flare` **unattended** on this machine so a block/escalate reaches
the operator's phone without anyone watching a terminal. flare's code is
delivery-capable (routes, dedupe, severity-monotone throttle, Slack + toast
channels); this doc is the machine wiring that makes "unattended notification"
an operational fact instead of a claim about code.

Two platforms, one shape. **Windows / PowerShell** is sections 1–6 below;
**darwin / launchd** is [its own section](#darwin--launchd) at the end and also
brings up `escalate serve`, the inbound half of the loop. Everything here is
machine config — none of it is committed (`~/.flare/`, Task Scheduler, and
`~/Library/LaunchAgents/` all live off-repo).

## Model in one line

`flare watch` is a foreground poll loop: every `poll_seconds` (default 60) it
reads each source from its cursor, routes what's new, and sleeps. A Scheduled
Task keeps that loop alive across logon/reboot; `flare status` is how you prove
it's still polling.

## Lifecycle: the `flare-task.ps1` script

`cmd/flare/scripts/flare-task.ps1` is the one entry point for every lifecycle
op. It wraps the raw PowerShell below and encodes the two things that bite you
by hand: the **UAC elevation** creating a task needs, and the **stop-before-
rebuild** ordering the Windows exe-lock forces.

| Command | Elevates? | Does |
|---|---|---|
| `flare-task.ps1 install`   | yes — self, 1 UAC prompt | register + start the watcher |
| `flare-task.ps1 update`    | no  | stop → `go install ./cmd/flare` → start (pick up new flare code) |
| `flare-task.ps1 restart`   | no  | restart the task (reload `routes.json` after an edit) |
| `flare-task.ps1 status`    | no  | task state + `flare status` |
| `flare-task.ps1 uninstall` | yes — self, 1 UAC prompt | stop + unregister |

`install` / `uninstall` self-elevate — a non-elevated token (even an admin's,
under UAC) cannot write the Task Scheduler store. The rest run unprivileged.
The numbered sections below are what the script does under the hood; reach for
them to understand or debug, not for day-to-day use.

## Prerequisites

- Go toolchain on PATH (to build).
- `~/.flare/routes.json` present and valid (sources, channels, routes). It is
  already configured on this machine — see [Configure the phone rung](#configure-the-phone-rung).

## 1. Install the binary

`flare` is not shipped as a release; install it onto PATH from the module.
**Build from `main`** — a feature branch that predates a channel merge produces
a binary whose config schema lags. A pre-#34 branch, for instance, has no
`slack` channel type, so `flare status` on a Slack `routes.json` dies at load
with `config: parse …: json: unknown field "token"`. Verify you're current
first:

```powershell
git -C <workbench> fetch origin
git -C <workbench> log --oneline HEAD..origin/main -- cmd/flare   # empty = your checkout has all flare channels
go install github.com/itsHabib/workbench/cmd/flare                # -> %USERPROFILE%\go\bin\flare.exe (builds current checkout)
flare status                                                      # smoke test; exits 1 if stale/never-run
```

If your working branch lags, build from a clean main checkout (or a detached
worktree at `origin/main`) so the installed binary matches your `routes.json`.

`~/go/bin` is already on PATH on this machine. Confirm the resolved path — the
Scheduled Task needs it absolute:

```powershell
(Get-Command flare).Source                            # e.g. C:\Users\<you>\go\bin\flare.exe
```

## 2. Register the always-on watcher

`flare-task.ps1 install` does all of this and **self-elevates**. Manual
equivalent — a logon-triggered task, restart-on-failure, one instance (run from
an **Admin** shell, or `Register-ScheduledTask` returns `Access is denied`):

```powershell
$exe = (Get-Command flare).Source
$action   = New-ScheduledTaskAction   -Execute $exe -Argument "watch"
$trigger  = New-ScheduledTaskTrigger  -AtLogOn
$settings = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1) `
              -MultipleInstances IgnoreNew -StartWhenAvailable -ExecutionTimeLimit ([TimeSpan]::Zero)
Register-ScheduledTask -TaskName "flare-watch" -Action $action -Trigger $trigger -Settings $settings `
  -Description "flare escalation watcher (workbench) — poll loop, pushes block/escalate to Slack/toast"
Start-ScheduledTask -TaskName "flare-watch"           # start now; don't wait for next logon
```

Notes:
- `-ExecutionTimeLimit 0` = no timeout; `watch` is meant to run forever.
- Runs in the interactive user session (logon trigger). That is deliberate:
  the **toast** channel shells out to `powershell.exe` 5.1 for WinRT, which
  needs a session. The **phone/Slack** channel is plain HTTP and would work
  headless, but the task carries both.
- A console window appears at logon. To hide it, wrap the action:
  `-Execute "conhost.exe" -Argument "--headless `"$exe`" watch"` (Windows 11),
  or a `powershell -WindowStyle Hidden -Command "flare watch"` shim.

## Updating flare after a code change

The task runs a **compiled binary** (`~/go/bin/flare.exe`), not `go run` — a
daemon shouldn't recompile on every restart. So a flare code change doesn't
reach the watcher until you rebuild and relaunch. Windows **locks a running
`.exe`**, so the order is fixed — stop, rebuild, start:

```powershell
flare-task.ps1 update      # stop -> go install ./cmd/flare -> start
```

Under the hood:

```powershell
Stop-ScheduledTask  -TaskName flare-watch          # releases the .exe lock
go install ./cmd/flare                             # run from the repo root
Start-ScheduledTask -TaskName flare-watch          # relaunch on the new binary
```

A **config-only** change (editing `routes.json`) needs no rebuild — just
`flare-task.ps1 restart`, which reloads the loop.

## 3. Configure the phone rung

The phone destination is a Slack channel post (`chat.postMessage`), not an
incoming webhook. It lives in `~/.flare/routes.json`:

```jsonc
"channels": {
  "phone": { "type": "slack", "token": "xoxb-…", "channel": "C0…" }   // bot token + channel id
},
"routes": [
  { "match": { "source": "gate", "kind": "escalation" }, "channel": "phone" }
]
```

Already wired on this machine (gate escalations → `phone`). To **rotate** the
token: edit `channels.phone.token`, then `Restart-ScheduledTask -TaskName flare-watch`
so the loop reloads config. Treat the token as a live credential — it is
plaintext on disk and sends event titles off-box to Slack.

## 4. Verify end-to-end

Run this matrix once after install; it exercises the pinned invariants against
the real channel, not unit tests.

| Check | Do | Pass condition |
|---|---|---|
| **Liveness** | `flare status` | `healthy: true`, `last_poll` within ~3 min; exit 0 |
| **Survives reboot** | reboot / log off + on, wait one poll, `flare status` | fresh `last_poll` with **no manual start** |
| **Delivery** | append a test escalation line to the gate log source (`gate/state/log.jsonl`) matching `{source: gate, kind: escalation}`, then `flare sweep` | a Slack notification lands on the phone; journal shows `Delivered` |
| **Retry** | temporarily break the Slack token, force a test event, `flare sweep` | delivery `Errored`, cursor held; fix token → next sweep delivers (no lost page) |
| **Throttle** | fire two same-source events, second strictly worse severity | the worse one passes the open window (worst-wins) |
| **Watcher-dead** | `Stop-ScheduledTask -TaskName flare-watch`, wait >3 polls, `flare status` | `healthy: false`, exit 1 (staleness is visible) |

Exact test-line shape for the gate source is in `cmd/flare/internal/source` +
`docs/DESIGN.md` (the source read shapes). `flare sweep` is the one-shot form
of a cycle — use it to test without waiting on the loop.

## 5. Read status / troubleshoot

`flare-task.ps1 status` rolls the task state and `flare status` into one. The raw pieces:

```powershell
flare status | ConvertFrom-Json | Format-List           # healthy, last_poll, per-source cursors, recent journal tail
Get-ScheduledTask -TaskName flare-watch | Get-ScheduledTaskInfo   # LastRunTime, LastTaskResult, next run
Get-Content "$env:USERPROFILE\.flare\journal.jsonl" -Tail 20     # delivery journal (dedupe substrate)
```

- `status` exit 1 = stale or never ran. Stale threshold = `3 × poll_seconds`.
- A stale `last_poll` with the task "Running" usually means the loop is blocked
  on a bad source path — check `routes.json` `sources[].path` exists.
- A `cursor-alert` in the journal means a source log shrank or its chain hash
  broke; flare resweeps rather than silently resetting.
- A `cursor-init` in the journal is where flare first looked at a source: the
  cursor was placed at the log's tail and nothing before it was delivered.
  `flare sweep -from-start` (before the watcher exists) is the opt-in to page
  the backlog.

## 6. Uninstall

`flare-task.ps1 uninstall` (self-elevates). Manual equivalent:

```powershell
Stop-ScheduledTask       -TaskName flare-watch
Unregister-ScheduledTask -TaskName flare-watch -Confirm:$false
```

State under `~/.flare/` is left in place (journal + cursors). Delete it only to
reset dedupe/liveness history.

## State + config locations

| Path | What |
|---|---|
| `~/.flare/routes.json` | sources, channels, routes, `poll_seconds`, `catch_all` (config; `-config` to override) |
| `~/.flare/cursors.json` | per-source read cursors + `last_poll` (the liveness fact `status` reads) |
| `~/.flare/journal.jsonl` | append-only delivery journal (dedupe substrate) |
| Task Scheduler `flare-watch` | the always-on `flare watch` loop (Windows) |
| `~/.flare/env` | darwin only — env file the LaunchAgents source (secrets; `chmod 600`) |
| `~/.flare/logs/` | darwin only — daemon stdout/stderr |
| `~/Library/LaunchAgents/com.workbench.*.plist` | darwin only — the two agents |

## darwin — launchd

The macOS equivalent of everything above, plus `escalate serve` so the Slack
**Approve/Block** buttons flare renders actually resolve a parked gate run.
Zero → running is `install`; the only things left blank are the Slack
credentials and the tunnel.

### The two agents

| Agent | Runs | Why it's here |
|---|---|---|
| `com.workbench.flare-watch` | `flare watch` | outbound: poll loop → Slack page |
| `com.workbench.escalate-serve` | `escalate serve` | inbound: signed Slack tap → `gate resolve` |

Both are **user LaunchAgents** in `~/Library/LaunchAgents/`, bootstrapped into
`gui/$UID`. No `sudo` anywhere — unlike Windows, where creating a Scheduled
Task needs an elevated token.

Templates live at `cmd/flare/scripts/com.workbench.flare-watch.plist` and
`…escalate-serve.plist`. They carry `__FLARE_BIN__` / `__ESCALATE_BIN__` /
`__GATE_BIN__` / `__HOME__` placeholders because **launchd expands neither `~` nor `$PATH` from
your shell profile** — the installed plist must be fully absolute. Never copy a
template into `~/Library/LaunchAgents/` by hand; the script renders it.

`__GATE_BIN__` is why `gate` must be installed before `flare-launchd.sh
install` — `escalate serve` shells the gate binary to run `gate resolve`, and
its `-gate` default is the bare name `gate`. Under launchd that resolves
against a PATH your shell profile never touched, and it fails **only at the
moment a human taps Approve**: the ingress reports healthy right up until the
one time it matters. So the script resolves gate to an absolute path at render
time and refuses to install if it can't.

### Lifecycle: the `flare-launchd.sh` script

`cmd/flare/scripts/flare-launchd.sh` is the one entry point, with the same five
verbs as `flare-task.ps1` on Windows. It acts on **both** agents together.

| Command | Does |
|---|---|
| `./flare-launchd.sh install` | render + `plutil -lint` + `launchctl bootstrap` both agents, then `status` |
| `./flare-launchd.sh update` | bootout → `go install ./cmd/flare ./cmd/escalate` → bootstrap (pick up new code) |
| `./flare-launchd.sh restart` | `launchctl kickstart -k` both (reload `routes.json` after an edit, no rebuild) |
| `./flare-launchd.sh status` | per-agent state / pid / last exit status + `flare status` + log tail hints |
| `./flare-launchd.sh uninstall` | bootout + remove the installed plists (state under `~/.flare/` is kept) |

There is a sixth, `render <dir>`, which renders and lints the plists into a
directory and touches launchd not at all. It exists so the template-substitution
path is exercisable — in review, in a scratch dir, on CI — without bootstrapping
a real service.

### Zero → running

```sh
mkdir -p ~/.flare/logs
go install ./cmd/flare ./cmd/escalate ./cmd/gate   # from the repo root; -> ~/go/bin
cp cmd/flare/scripts/routes.example.json ~/.flare/routes.json
$EDITOR ~/.flare/routes.json                   # fill token + channel id + real source paths
$EDITOR ~/.flare/env                           # see "Secrets" below
chmod 600 ~/.flare/env
./cmd/flare/scripts/flare-launchd.sh install
```

`routes.example.json` is the shipped starting point (a gate-log source and a
ship-receipts source, gate escalations → a `resolve_actions: true` Slack
channel, a non-drop `catch_all`). A test in `cmd/flare/internal/config` loads it
every CI run, so it cannot silently drift out of schema with the binary — the
failure mode that once took this plane dark for ~17h.

Build from **`main`**, for the same reason the Windows section gives: a branch
predating a channel merge produces a binary whose config schema lags the routes
file, and `DisallowUnknownFields` rejects the whole file at load.

### Secrets — `~/.flare/env`

No secret goes in a plist. Each agent's `ProgramArguments` is a wrapper —
`sh -c 'set -a; . ~/.flare/env; exec <bin> <verb>'` — so credentials live in one
0600 file that is never committed and never read by anything but the daemons.

```sh
# ~/.flare/env — chmod 600, never committed
GATE_STATE=/Users/<you>/dev/gate/state
GATE_KEY=/Users/<you>/dev/gate/keys               # key CUSTODY DIR, not a key file;
                                                  # must be outside GATE_STATE
SLACK_SIGNING_SECRET=...                          # Slack app -> Basic Information
ESCALATE_ALLOWED_SLACK_USERS=U0123ABC,U0456DEF    # Slack user ids allowed to resolve
```

`escalate serve` **refuses to start** without `SLACK_SIGNING_SECRET` and
`ESCALATE_ALLOWED_SLACK_USERS` — an unauthenticated ingress would accept forged
decisions, and a signed callback alone only proves Slack sent it, not that the
tapper may move a merge gate. With `KeepAlive` that refusal shows up as a
restart loop with the reason in `~/.flare/logs/escalate-serve.err.log`. `status`
reports the non-zero last exit **and** names which of the two variables is
missing, so the failure reads as a configuration gap rather than an exit code to
decode. **`flare watch` is unaffected** — the outbound page works
before the inbound ingress is wired.

To rotate the Slack bot token, edit `~/.flare/routes.json`; to rotate the
signing secret or the allowlist, edit `~/.flare/env`. Either way:
`./flare-launchd.sh restart`. No rebuild.

### Day 2

- **Changed flare/escalate code** → `./flare-launchd.sh update`. The agents hold
  the compiled binary open, so the order is fixed: stop, `go install`, start. A
  failed build still brings the previous binaries back up and then reports the
  failure — the plane never stays dark because a build broke.
- **Edited `routes.json`** → `./flare-launchd.sh restart`. No rebuild.
- **Is it alive?** → `./flare-launchd.sh status`. `flare status` exits 1 when
  stale (threshold `3 × poll_seconds`); the agent block above it shows whether
  launchd thinks the process is running.
- **Logs** → `tail -f ~/.flare/logs/flare-watch.err.log` (and the
  `escalate-serve` pair). `launchctl print gui/$UID/com.workbench.flare-watch`
  is the raw form of what `status` summarizes.

### The tunnel

`escalate serve` listens on `127.0.0.1:8099` by default — loopback, deliberately.
Slack's interactivity Request URL must reach it, which needs a public ingress:
`cloudflared tunnel --url http://127.0.0.1:8099` or `ngrok http 8099`, with the
tunnel's public URL set as the Slack app's **Interactivity & Shortcuts → Request
URL**. `escalate serve` handles the callback on any path, so the tunnel root is
fine.

That tunnel is **not** managed here. It is a separate account-bound credential
with its own lifecycle, and a free-tier URL that rotates on every restart would
silently break the Request URL anyway — so wiring it into this script would
manufacture the appearance of an always-on ingress that isn't one. Run it in a
terminal while resolving, or give it its own LaunchAgent modeled on these two
once you have a stable named tunnel.

`routes.example.json` ships `resolve_actions: true` because a live tap is the
point of this slice. If you stand flare up **before** the Request URL is wired,
set it back to `false`: a rendered button with no ingress is a dead tap ("this
app is not configured to handle interactions"). The flag is the operator's
signal that the ingress exists — flare renders only, and never handles the tap.

### What is manual-only

`install` / `restart` / `uninstall` touch real launchd state and are verified by
hand on the machine. Automated coverage stops at the seam: the config test loads
`routes.example.json`, and `flare-launchd.sh render <dir>` proves the template
substitution and `plutil -lint` path without bootstrapping a service.

### Uninstall

```sh
./flare-launchd.sh uninstall
```

Boots both agents out and removes the installed plists. `~/.flare/` (journal,
cursors, env, logs) is left in place; delete it only to reset dedupe/liveness
history.
