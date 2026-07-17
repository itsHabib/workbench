# workbench-mcp

The unified workbench MCP surface (v0): a JSON-RPC 2.0 server over **stdio**
exposing the four driver-state verbs — `driver_record`, `driver_state`,
`driver_runs`, `driver_verify`. It is the **client boundary** of the
driver-state plane (spec `docs/features/driver-state/spec.md` §6, §11); the P3
validation gate runs against it.

A workbench tenant: the binary is `cmd/workbench-mcp`, the guts are private under
`cmd/workbench-mcp/internal/server`. It imports at most `driverstate` (the ledger
mechanism) + `contracts` (the shared vocabulary) — no other tool (charter
boundary law).

## What it does

- **Transport + verb dispatch** over the mechanism. Mechanism (ledger, leases,
  hash chain) stays in `driverstate`; this tool is policy over it.
- **Session-lifetime lease.** It claims a run's lease lazily on first
  `driver_record` and holds it, auto-renewing on a background goroutine at
  `DefaultLeaseTTL / 2`, for as long as the client session is connected. There
  is deliberately **no `driver_renew` verb**. Server exit (stdin EOF) releases
  the leases; an orphan self-expires within one TTL window regardless.
- **Canonical state root, printed.** It resolves the state dir ONCE at startup
  via `driverstate.StateRoot` (`WORKBENCH_STATE_DIR`, else the user profile) and
  prints it to **stderr** — two instances resolving different roots is the
  ship/MSIX failure mode the plane exists to kill (spec §6 P2). stdout carries
  only the JSON-RPC channel.
- **Compile-time verb registration.** `verbs.go` IS the allowlist. Nothing
  capability-mutating (grant minting) has an entry, so it cannot be reached.
  Unknown verbs return JSON-RPC `MethodNotFound`. Structured ledger errors
  (`ErrIllegalTransition` / `ErrChainBroken` / `ErrLocked`) come back as
  `isError` tool results so a driving agent sees and corrects them (spec §7 F2).

## Register

`.mcp.json` (project or user scope), with `WORKBENCH_STATE_DIR` in the server
env when a non-default root is wanted:

```json
{ "mcpServers": { "workbench": { "command": "workbench-mcp",
  "env": { "WORKBENCH_STATE_DIR": "/path/to/driver-state" } } } }
```

## Checks

```
gofmt -l . && go vet ./... && golangci-lint run ./... && go test ./cmd/workbench-mcp/...
```
