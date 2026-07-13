# Portfolio Control Room

Control Room is a local, read-only portfolio operations surface. It can serve a deterministic demo or compose current observations from Ship, Dossier, GitHub, Tracelens, tool-health, and optional Tower adapters.

## Run the demo

```powershell
go run ./cmd/controlroom serve --mode demo --addr 127.0.0.1:4317
```

Open the printed loopback URL in a browser. Use `127.0.0.1`, not `localhost`: the server rejects any Host or Origin outside its exact bound IPv4 loopback address. Run one server per browser profile because the CSRF cookie is host-scoped.

To choose an available port, pass `--addr 127.0.0.1:0`; the command prints the selected canonical URL after it binds.

## Inspect the snapshot

```powershell
go run ./cmd/controlroom snapshot --mode demo --json
```

The command writes the same policy-applied snapshot consumed by the browser. Its clock and relative ages are fixed, so output and screenshots remain reproducible.

## Run against real sources

Real mode requires explicit absolute workspace and Dossier corpus paths plus one to four GitHub scopes. Executable flags default to PATH names; Tower is disabled unless its executable is supplied.

```powershell
go run ./cmd/controlroom serve --mode real --addr 127.0.0.1:4317 `
  --workspace-root C:\portfolio `
  --dossier-corpus C:\portfolio\dossier-state `
  --github-scope user:synthetic-author
```

Use the same source flags with `snapshot --mode real --json` for one bounded collection without the browser timer. A source failure does not hide healthy panels: the current attempt is marked unavailable and any retained payload is separately marked stale.

## Current boundary

Production code uses only the Go standard library and embedded same-origin assets. The browser reads an immutable snapshot supplier and calls a narrow refresh callback. Source adapters invoke owner CLIs/MCP but never read producer stores directly, mutate planning or workflow state, or expose arbitrary local files.

Runs expose owner status alongside operator state, current stage, exact and relative last durable update, failure class, evidence, and a conservative next action. Waiting owner boundaries take precedence over the stall timer; failures never imply retry or resume is safe. Pinned Playwright automation, committed screenshots, and the full [operator runbook](../../docs/features/portfolio-control-room/runbook.md) live with the feature documentation.

## Validate

```powershell
gofmt -l .
go vet ./...
golangci-lint run ./...
go test ./...
go build ./...
npm --prefix cmd/controlroom/e2e ci
npm --prefix cmd/controlroom/e2e test
git diff --check
```

The browser suite runs both a 1440×900 laptop viewport and a 390×844 narrow viewport. It covers demo and real-mode shells, staged core/enrichment publication, filters, drawers, partial failure, disconnection, and unattended-run operator states. Regenerate the three release screenshots with `npm --prefix cmd/controlroom/e2e run screenshots`.
