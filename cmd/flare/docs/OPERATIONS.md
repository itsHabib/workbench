# flare — operations runbook

How to run `flare` **unattended** on this machine so a block/escalate reaches
the operator's phone without anyone watching a terminal. flare's code is
delivery-capable (routes, dedupe, severity-monotone throttle, Slack + toast
channels); this doc is the machine wiring that makes "unattended notification"
an operational fact instead of a claim about code.

Windows / PowerShell. Everything here is machine config — none of it is
committed (`~/.flare/` and Task Scheduler live off-repo).

## Model in one line

`flare watch` is a foreground poll loop: every `poll_seconds` (default 60) it
reads each source from its cursor, routes what's new, and sleeps. A Scheduled
Task keeps that loop alive across logon/reboot; `flare status` is how you prove
it's still polling.

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

Logon-triggered task, restart-on-failure, one instance:

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

## 6. Uninstall

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
| Task Scheduler `flare-watch` | the always-on `flare watch` loop |
