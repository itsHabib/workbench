// Command workbench-mcp is the unified workbench MCP surface (v0): a JSON-RPC 2.0
// server over stdio exposing the four driver-state verbs — driver_record,
// driver_state, driver_runs, driver_verify.
//
// It resolves the canonical state root once at startup (WORKBENCH_STATE_DIR,
// else the user profile) and PRINTS it to stderr, because two instances
// resolving different roots is the ship/MSIX failure mode the plane exists to
// kill (spec §6 P2). stdout carries only the JSON-RPC channel; all logging goes
// to stderr.
//
// Register it in .mcp.json (project or user scope) with WORKBENCH_STATE_DIR in
// the server env when a non-default root is wanted.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/itsHabib/workbench/cmd/workbench-mcp/internal/server"
	"github.com/itsHabib/workbench/driverstate"
)

func main() {
	dir, source, err := driverstate.StateRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "workbench-mcp:", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "workbench-mcp: state root: %s (source: %s)\n", dir, source)

	srv := server.New(dir)
	if err := srv.Serve(context.Background(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "workbench-mcp:", err)
		os.Exit(1)
	}
}
