# driverstate (CLI)

The human/cron **CLI mirror** of the `workbench-mcp` driver verbs — the 1:1 twin
of the MCP surface (spec `docs/features/driver-state/spec.md` §6). Same state
root, same validation, same ledger; different only in lifecycle.

A workbench tenant: the binary is `cmd/driverstate`. It imports at most
`driverstate` + `contracts` — no other tool (charter boundary law).

## Verbs

```
driverstate record [--run <id>] [--json]   < event.json
driverstate state  --run <id> [--json]
driverstate runs   [--repo <r>] [--live] [--json]
driverstate verify --run <id> [--json]
```

- `record` reads an event as JSON on stdin, fills the client-minted defaults
  (run — explicit `--run`, else minted for `run_imported`; event id; time),
  then claims the run lease, appends, and **releases in one shot** — the no-session
  human/cron model (the server, by contrast, holds the lease for a session).
- `state` / `runs` / `verify` are pure reads.
- `--json` emits the reduced contract types verbatim, so CLI and MCP outputs
  carry the same shape and fields — the CLI indents for humans, the MCP server
  emits compact JSON, so they are not byte-for-byte identical.

## State root

Resolved the SAME way the server resolves it — `driverstate.StateRoot`
(`WORKBENCH_STATE_DIR`, else the user profile) — so a terminal CLI and an MCP
client never diverge on where the ledger lives (spec §6 P2). The resolved root
prints to **stderr**; stdout carries only the command output, so `--json` stays
clean.

## Checks

```
gofmt -l . && go vet ./... && golangci-lint run ./... && go test ./cmd/driverstate/...
```
