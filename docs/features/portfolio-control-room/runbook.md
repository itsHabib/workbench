# Portfolio Control Room runbook

Portfolio Control Room is a loopback-only, read-only view of current Ship, Dossier, GitHub, Tracelens, tool-health, and optional Tower facts. It does not mutate producer state, run as a daemon, persist snapshots, or decide that a retry/resume is safe.

## Prerequisites

- Go from the version declared in the repository `go.mod`.
- An explicit absolute portfolio workspace path.
- An explicit absolute Dossier corpus path.
- One to four GitHub scopes in `repo:owner/name`, `org:name`, or `user:name` form.
- Owner CLIs on `PATH` when their panels should be live. Missing CLIs qualify only their own source; they do not prevent the server from starting.
- `gh` authentication for GitHub-backed facts.

The server accepts only an IPv4 loopback bind. Keep source stores, credentials, and private traces outside the repository.

## Start the deterministic demo

```powershell
go run ./cmd/controlroom serve --mode demo --addr 127.0.0.1:4317
```

Open the printed URL. The demo clock is fixed at `2026-07-13T12:00:00Z`; refresh changes only the publication version, so filters, screenshots, and operator-state examples remain reproducible.

For a non-browser contract check:

```powershell
go run ./cmd/controlroom snapshot --mode demo --json
```

## Start real mode

```powershell
go run ./cmd/controlroom serve --mode real `
  --addr 127.0.0.1:4317 `
  --workspace-root D:\portfolio `
  --dossier-corpus D:\dossier-state `
  --github-scope repo:owner/repository
```

Executable flags default to `ship`, `dossier`, `gh`, `tracelens`, and `toolhealth`. Tower is disabled unless `--tower-executable` is supplied. A configured command that cannot be resolved is retained in the configuration fingerprint and becomes an unavailable receipt owned by that adapter.

Real refreshes publish in two stages:

1. Ship, Dossier, GitHub, and optional Tower form the core generation under a 15-second budget.
2. Tracelens and tool-health enrich that accepted generation under a separate 35-second budget.

The browser renders the core generation immediately and polls until diagnostics settle. A new refresh supersedes a different in-flight identity; an identical refresh joins it. The browser schedules the next automatic refresh 60 seconds after settlement or visible failure.

## Read source qualifications

| Source state | Meaning | Operator use |
|---|---|---|
| `ok` | Current owner facts were collected. | Use the panel normally. |
| `degraded` | Bounded or partial facts are usable. | Read the receipt message before drawing a negative-evidence conclusion. |
| `loading` | This publication is awaiting the source. | Treat the panel as unsettled. |
| `stale` | Retained rows came from the last accepted generation. | Use for context only; stale support cannot create a higher-consequence action. |
| `unavailable` | The source could not provide facts in budget. | Other panels remain usable; troubleshoot only this source. |

The Sources panel is the first troubleshooting stop. It reports observed time, duration, stable error code, sanitized message, and what remains usable. Missing Tower or tool-health must never blank healthy Ship/Dossier/GitHub panels.

## Read unattended runs

Never act from producer status alone. Use the run's operator state, current stage, exact and relative last durable update, failure class, evidence, and next action together.

| Operator state | Interpretation | Safe next step |
|---|---|---|
| `progressing` | An active run has updated inside the 15-minute freshness window. | Monitor for the next durable update. |
| `waiting` | Status or stage names a review, approval, judgment, or merge-authority boundary. | Inspect the named boundary and its owner; waiting outranks the stall clock. |
| `stalled` | An active run has no durable update for at least 15 minutes. | Inspect current-stage evidence and ownership before intervening. |
| `failed` | The owner reports a terminal failure, cancellation, error, or timeout. | Inspect failure evidence and ownership before deciding whether retry is safe. |
| `done` | The owner reports terminal success. | No action is required. |
| `unknown` | Ship truth is non-current or the owner status is not mapped. | Revalidate Ship source truth before acting. |

`timed_out` is a terminal failure, not an unknown or still-running state. Control Room deliberately has no retry/resume button: owner contracts do not currently prove that arbitrary retries are idempotent or safe.

## Common incidents

- **Disconnected banner:** the initial refresh or snapshot request failed. Retry the connection; if an older snapshot exists it remains visible and explicitly qualified.
- **Diagnostics remain loading:** collection exceeded the browser's roughly 54-second observation window. Read the Tracelens/tool-health receipts and the server terminal; optional diagnostics do not extend the global budget.
- **`executable_not_found` / unresolved command:** install the owner CLI or pass its explicit executable path, then refresh. Other sources should remain visible.
- **GitHub unavailable:** confirm `gh auth status` and the scope spelling. Do not infer merge readiness from missing checks/reviews.
- **Dossier unavailable:** confirm the corpus absolute path and owner process. Automatic refresh does not force a half-open breaker probe; a manual refresh may perform the single permitted probe.
- **Port in use:** choose another loopback port with `--addr 127.0.0.1:<port>`.

Stop the server with `Ctrl+C`. All in-memory retained snapshots disappear on process exit.

## Validate a fresh checkout

```powershell
go vet ./...
golangci-lint run ./...
go test -race ./...
go build ./...
npm --prefix cmd/controlroom/e2e ci
npx --prefix cmd/controlroom/e2e playwright install chromium
npm --prefix cmd/controlroom/e2e test
```

Linux CI installs Chromium with system dependencies via `playwright install --with-deps chromium`. Production Go remains standard-library-only; Node and Playwright are confined to `cmd/controlroom/e2e`.

Regenerate the canonical 1440×900 evidence images with:

```powershell
npm --prefix cmd/controlroom/e2e run screenshots
```

Review all three files under `docs/features/portfolio-control-room/screenshots/` before committing them.
