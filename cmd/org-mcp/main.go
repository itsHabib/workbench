// Command org-mcp is the MCP surface of the Baton home: a stdio JSON-RPC
// server exposing the org verbs as native tools, so any agent session can
// boot, claim, yield and checkpoint a role without knowing the CLI exists.
//
// It shells the org binary (ORG_BIN, default "org") and inherits ORG_STATE /
// ORG_TENANT from its own environment — the same resolution the CLI does, so
// the two surfaces can never disagree about which home they speak to. It owns
// no state and makes no decision: the CLI's exit-code seam is the whole
// contract, and a kernel refusal surfaces to the agent as an isError result
// carrying the refusal reason.
//
// Register in .mcp.json:
//
//	{ "mcpServers": { "org": { "command": "org-mcp",
//	  "env": { "ORG_STATE": "/Users/you/dev/org/state" } } } }
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/itsHabib/workbench/cmd/org-mcp/internal/server"
)

func main() {
	bin := os.Getenv("ORG_BIN")
	if bin == "" {
		bin = "org"
	}
	fmt.Fprintf(os.Stderr, "org-mcp: serving over stdio, shelling %s (state %s)\n", bin, stateHint())
	s := server.New(server.Shell(bin))
	if err := s.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "org-mcp:", err)
		os.Exit(1)
	}
}

// stateHint reports where the child org processes will resolve their state,
// for the startup line only — resolution itself stays the CLI's.
func stateHint() string {
	if v := os.Getenv("ORG_STATE"); v != "" {
		return v
	}
	return "default (~/dev/org/state)"
}
