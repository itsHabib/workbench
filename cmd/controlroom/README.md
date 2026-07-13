# Portfolio Control Room

Control Room is a local, read-only portfolio operations surface. The Phase 3 command serves a deterministic demo snapshot so the full healthy-to-on-fire story can be reviewed before real source adapters are connected.

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

## Current boundary

Production code uses only the Go standard library and embedded same-origin assets. The browser reads a snapshot supplier and calls a narrow refresh callback; it does not import producer packages, read producer stores, launch subprocesses, mutate planning or workflow state, or expose arbitrary local files.

Phase 3 accepts only demo mode. Real Ship, Dossier, GitHub, TraceLens, tool-health, and optional Tower adapters arrive in the next phase; background generation coordination arrives after that. Pinned Playwright automation, committed screenshots, and the operator runbook are part of final hardening.

## Validate

```powershell
gofmt -l .
go vet ./...
golangci-lint run ./...
go test ./...
go build ./...
git diff --check
```
