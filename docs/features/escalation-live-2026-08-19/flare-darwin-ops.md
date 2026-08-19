**Status**: draft
**Owner**: @mh
**Date**: 2026-08-19
**Related**: dossier task `flare-darwin-ops` (id: `tsk_01M0DCXK4DEDG5ZPPP07K0YNVE`), phase `escalation-live-2026-08-19`

# darwin ops slice: launchd plists + runbook for flare watch and escalate serve — design spec

## Scope

| Bucket | Files | Est. LOC | Weighted |
|---|---|---|---|
| Ops assets | `cmd/flare/scripts/*.plist`, install script, `routes.example.json` | ~180 | 180 |
| Docs | `cmd/flare/docs/OPERATIONS.md` (darwin section) | ~120 | 60 |
| **Total** | | | **~240** |

Band: **ideal** per repo's PR sizing convention.

## Goal

flare and escalate are built and fully green but the operations wiring
(`cmd/flare/docs/OPERATIONS.md`, `cmd/flare/scripts/flare-task.ps1`) exists only
for Windows Task Scheduler. On darwin the notification plane is dark. Deliver the
darwin equivalent so the operator can go from zero to a running `flare watch` +
`escalate serve` with only Slack-app/tunnel secrets left to fill in.

## Behavior

- **launchd plists** (user LaunchAgents, `~/Library/LaunchAgents/`), committed as
  templates under `cmd/flare/scripts/`:
  - `com.workbench.flare-watch.plist` — runs `flare watch`; `KeepAlive`, stdout/err
    log paths under `~/.flare/logs/`.
  - `com.workbench.escalate-serve.plist` — runs `escalate serve`; same conventions.
  - Env wiring: `GATE_STATE`, `GATE_KEY`, `SLACK_SIGNING_SECRET`,
    `ESCALATE_ALLOWED_SLACK_USERS` sourced from a non-committed env file
    (e.g. `~/.flare/env`) — never inline secrets in the plist. Use a shell wrapper
    (`/bin/sh -c 'set -a; . ~/.flare/env; exec ...'`) or equivalent.
- **Helper script** `cmd/flare/scripts/flare-launchd.sh` with `install`,
  `uninstall`, `status` verbs, mirroring what `flare-task.ps1` does on Windows:
  render templates with the resolved binary path, `plutil -lint`, `launchctl
  bootstrap gui/$UID` / `bootout`, and a status view (`launchctl print` + log tail
  hint).
- **Routes template** `cmd/flare/scripts/routes.example.json` (example only, real
  config stays in `~/.flare/routes.json`): gate escalation source (gate state log)
  routed to a Slack channel with `resolve_actions: true`, plus ship
  `receipts.jsonl` source; `catch_all` present as config requires.
- **Docs**: a darwin section in `cmd/flare/docs/OPERATIONS.md` (or sibling
  `OPERATIONS-darwin.md` if the doc's structure prefers): install, verify, tail
  logs, rotate secrets, and a tunnel note (ngrok/cloudflared runs separately or
  gets its own plist — document the choice, don't force it).

Note: /health board wiring for flare already exists (health skill reads
`~/.flare/cursors.json` / `journal.jsonl` directly) — out of scope here.

## Acceptance

- `plutil -lint` passes on both rendered plists.
- `flare-launchd.sh install` / `status` / `uninstall` round-trips cleanly on this
  Mac (no Slack secrets needed to verify the services start and log; escalate
  serve may exit-fail on missing secrets — status must surface that legibly, not
  hide it).
- `routes.example.json` parses against `cmd/flare/internal/config` (add a small
  test loading the example file).
- Docs walk zero → running with only secrets left blank.

## Test plan

- Config-parse test for `routes.example.json` in `cmd/flare/internal/config`.
- Script logic that is testable without launchd (template rendering, lint) covered
  by a shell-invoking Go test or a script self-check mode; document what is
  manual-only.

## Non-goals

- Creating the Slack app, tokens, or tunnel (operator-only, tracked outside).
- Any change to flare/escalate runtime behavior.
- Windows runbook changes.
