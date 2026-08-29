# org-mcp

The MCP surface of the Baton home: a stdio JSON-RPC 2.0 server exposing the
org verbs as native agent tools (`org_boot`, `org_status`, `org_sweep`, `org_attach`,
`org_claim`, `org_yield`, `org_complete`, `org_abandon`, `org_assign`,
`org_unassign`, `org_intent`, `org_resolve`, `org_escalate`, `org_note`,
`org_checkpoint`, `org_release`, `org_verify`).

A workbench tenant: the binary is `cmd/org-mcp`, guts private under
`cmd/org-mcp/internal/server`. It **shells the org binary** (`ORG_BIN`,
default `org`) and composes on its exit-code + JSON-receipt seam — it never
imports the home, owns no state, and makes no decision (charter boundary law;
same posture as console/escalate over gate).

## Invariants

- **The verb table IS the allowlist.** Role-structure verbs — charter,
  takeover, revoke, retire, recharter, delegate — have no entry and cannot be
  reached over MCP. Reshaping the org stays a deliberate operator act at the
  CLI.
- **Refusals surface, never vanish.** org exit 1 maps to an `isError` tool
  result `{code: "refused", reason: <kernel reason id>, detail: <stderr>}` so
  a driving agent can branch on `dangling_claim` exactly as a script branches
  on the exit code. Exit ≥2 maps to `{code: "error", exit, detail}`.
- **One resolution.** State and tenant come from the server's own
  environment (`ORG_STATE` / `ORG_TENANT`), inherited by every child org
  process — the CLI and MCP surfaces cannot disagree about which home they
  speak to.
- Verb handlers are pure translation (MCP arguments → CLI flags); the CLI's
  flag parsing and the kernel's laws stay the single source of truth.

## Register

```json
{ "mcpServers": { "org": { "command": "org-mcp",
  "env": { "ORG_STATE": "/Users/you/dev/org/state" } } } }
```

## Checks

```
gofmt -l ./cmd/org-mcp && go vet ./cmd/org-mcp/...
golangci-lint run ./cmd/org-mcp/...
go test ./cmd/org-mcp/...
```
