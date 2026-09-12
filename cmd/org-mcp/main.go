// Command org-mcp exposes role-card registration and reads over MCP.
// It shells org and inherits ORG_STATE / ORG_TENANT; it owns no state.
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
