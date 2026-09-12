# org-mcp

The three Org role-card operations as native MCP tools: `org_charter`, `org_boot`
and `org_status`. This server shells `org` (`ORG_BIN` override) and inherits
`ORG_STATE` and `ORG_TENANT`. It owns transport, not another registry.

Registering or editing a card is configuration, not an authority grant. Journal,
work and messaging operations are absent from this surface. Old lifecycle callers
are removed during cutover. Upgrade both binaries
together; see [the Org guide](../org/README.md).

Argument translation stays here; card storage and validation stay in Org.
CLI errors become `isError` tool results. Never silently ignore a failed write.

```json
{ "mcpServers": { "org": { "command": "org-mcp",
  "env": { "ORG_STATE": "/Users/you/dev/org/state" } } } }
```

Checks: `gofmt -l ./cmd/org-mcp`, `go vet ./cmd/org-mcp/...`,
`golangci-lint run ./cmd/org-mcp/...`, `go test ./cmd/org-mcp/...`.
