// Command driverstate is the human/cron CLI mirror of the workbench-mcp driver
// verbs: record | state | runs | verify, each with --json. It is the 1:1 CLI
// twin of the MCP surface — same state root, same validation, same ledger.
//
// It resolves the canonical state root the SAME way the server does — via
// driverstate.StateRoot (WORKBENCH_STATE_DIR, else the user profile) — so a
// terminal CLI and an MCP client never diverge on where the ledger lives
// (spec §6 P2). The resolved root is printed to stderr; stdout carries only the
// command's own output, so --json stays clean.
//
//	driverstate record [--run <id>] [--json]   < event.json
//	driverstate state  --run <id> [--json]
//	driverstate render --run <id>
//	driverstate runs   [--repo <r>] [--live] [--json]
//	driverstate verify --run <id> [--json]
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/itsHabib/workbench/driverstate"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "driverstate:", err)
		os.Exit(1)
	}
}

// run dispatches the subcommand. It resolves and prints the state root once, so
// every verb writes and reads the one canonical ledger.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: driverstate record|state|render|runs|verify [flags]")
	}
	dir, source, err := driverstate.StateRoot()
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "driverstate: state root: %s (source: %s)\n", dir, source)

	cmd, rest := args[0], args[1:]
	switch cmd {
	case "record":
		return cmdRecord(dir, rest, stdin, stdout)
	case "state":
		return cmdState(dir, rest, stdout)
	case "render":
		return cmdRender(dir, rest, stdout)
	case "runs":
		return cmdRuns(dir, rest, stdout)
	case "verify":
		return cmdVerify(dir, rest, stdout)
	default:
		return fmt.Errorf("unknown command %q (want record|state|render|runs|verify)", cmd)
	}
}
