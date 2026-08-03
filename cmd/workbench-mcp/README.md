# workbench-mcp

The unified workbench MCP surface (v0): one Go binary that speaks JSON-RPC 2.0
over **stdio** and exposes six driver-state verbs — `driver_record`,
`driver_transition`, `driver_state`, `driver_runs`, `driver_verify`,
`driver_rollup` — as MCP tools, so an agent session can append to (and read
back) the hash-chained driver-state ledger without shelling anything. It
resolves the canonical state root once at startup via `driverstate.StateRoot`
(`WORKBENCH_STATE_DIR`, else `~/.workbench/driver-state`) and prints it to
stderr; stdout carries only the JSON-RPC channel. It claims a run's lease lazily
on the first write and auto-renews it on a background goroutine for the life of
the client session, releasing on stdin EOF.

A workbench tenant: the binary is `cmd/workbench-mcp`, the guts are private under
`cmd/workbench-mcp/internal/server`. The load-bearing seam is the **stdio
JSON-RPC channel plus the compile-time verb registry** in
`internal/server/verbs.go` — that slice IS the allowlist. It imports at most
`driverstate` (the ledger mechanism) and `contracts` (the shared vocabulary), no
other tool.

## Run it

```bash
go build ./cmd/workbench-mcp
WORKBENCH_STATE_DIR=/abs/path/to/driver-state ./workbench-mcp   # reads stdin
```

There are no command-line flags — `main.go` parses none. The only configuration
is `WORKBENCH_STATE_DIR`, and it **must be absolute** (a relative value is
rejected by `driverstate.StateRoot`, because two surfaces resolving different
roots is the failure mode the plane exists to kill).

Register it as an MCP server in `.mcp.json` (project or user scope):

```json
{ "mcpServers": { "workbench": { "command": "workbench-mcp",
  "env": { "WORKBENCH_STATE_DIR": "/abs/path/to/driver-state" } } } }
```

The handshake reports `protocolVersion` `2024-11-05`, `serverInfo`
`{"name":"workbench-mcp","version":"0.1.0"}`, and `tools`-only capabilities —
no resources, no prompts. The methods implemented are `initialize`, `ping`,
`tools/list`, and `tools/call`; anything else returns JSON-RPC
`MethodNotFound` (-32601). Framing is one JSON object per line, bounded at
8 MiB so a large `run_imported` manifest is not truncated.

## Tools

| Tool | Returns |
| --- | --- |
| `driver_record` | The sealed appended event, or a structured error. Low-level: the caller supplies the whole event and the `evt_` id. |
| `driver_transition` | The sealed event for a lifecycle transition built from flat facts; the server mints a deterministic id from the transition's natural key, so an identical retry dedupes. |
| `driver_state` | The reduced `RunState` for a run — run record plus per-stream derived status. |
| `driver_runs` | Run summaries, filterable by `repo`, `live` (unfinished only), and `parent`. |
| `driver_verify` | `{"run":…,"ok":true}` when the run's hash chain verifies, else the `ErrChainBroken` detail. |
| `driver_rollup` | The parent↔child join: one row per stream with the parent's mirrored status, the child's own status, the PR, per-child friction, and whether the mirror agrees. |

A registered verb's own failure comes back as an `isError` tool result carrying
a stable code — `ErrIllegalTransition`, `ErrLocked` (with `holder`),
`ErrChainBroken`, or generic `error` — not as a transport error, so a driving
agent can branch on it. An *unregistered* tool name is `MethodNotFound`.

## Exit codes

| Code | Meaning |
| ---: | --- |
| 0 | stdin reached EOF; session leases released |
| 1 | state-root resolution failed, or the serve loop returned an error |

That is the whole contract — there are only these two paths in `main.go`.

## Constraints that are design decisions, not omissions

- **It shells nothing and imports no other tool.** All work goes through the
  `driverstate` package in-process. It cannot cause a merge, cannot call
  `gate`, and has no network transport of any kind.
- **Not read-only, but not capability-mutating.** `driver_record` and
  `driver_transition` append to the ledger; the ledger is append-only and
  hash-chained. Nothing that mints capability (grants) has a registry entry, so
  it cannot be reached over MCP — exclusion is by construction, not by a check.
- **Stdio only.** No HTTP/SSE transport exists in the code. One client session
  per process.
- **No `driver_renew` verb, deliberately.** The lease is session-lifetime:
  auto-renewed at `DefaultLeaseTTL / 2` while connected, released on exit, and
  self-expiring within one TTL window if the process is killed.
- **Context cancellation is observed between messages only** — the read blocks
  in the scanner, so a hard mid-read shutdown requires closing stdin.

## Status

v0. The six verbs, the stdio loop, the lease renewer, and the structured-error
path are implemented and unit-tested under
`cmd/workbench-mcp/internal/server/`. The feature spec is marked **draft** and
its acceptance criteria (including the cross-client same-state-root check) are
stated as satisfiable preconditions, not as verified results — treat the P3
validation gate as unproven here. `cmd/driverstate` is the CLI mirror
(`record|state|render|runs|rollup|verify`) over the same state root.

Scoped guidance: [CLAUDE.md](CLAUDE.md). Spec:
[docs/features/workbench-mcp-v0/workbench-mcp-v0-server.md](../../docs/features/workbench-mcp-v0/workbench-mcp-v0-server.md),
over the locked design in `docs/features/driver-state/spec.md` §6, §11.
