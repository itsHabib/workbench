// Command org registers editable role cards. A role is configuration, not a
// session, a work claim, a mailbox, or an authority grant.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return codeUsage
	}
	e := &env{stdin: stdin, stdout: stdout, stderr: stderr}
	var err error
	switch args[0] {
	case "charter":
		err = registerCard(e, args[1:])
	case "boot":
		err = readCard(e, args[1:])
	case "status":
		err = listCards(e, args[1:])
	default:
		fmt.Fprintf(stderr, "org: %q is not a role-card command. Work, mail and handoffs belong to their runtime. Only charter, boot and status are supported.\n", args[0])
		usage(stderr)
		return codeUsage
	}
	if err != nil {
		fmt.Fprintf(stderr, "org: %v\n", err)
		return codeError
	}
	return codeOK
}

func usage(w io.Writer) {
	fmt.Fprintln(w, `usage: org <charter|boot|status> [flags]

org charter -role <name> -file <card.md> [-parent <name>]
    Register or update a role. Edit its Markdown file to change its instructions.
org boot -role <name> [-max-bytes <n>]
    Read the current card; no attach, claim or checkpoint is required.
org status
    List registered roles and their optional parent references.

All commands accept -state <dir>, -tenant <id> and -json.
Parent references describe the directory; they grant no authority or messaging rights.
Old lifecycle commands are removed; update callers to role cards and runtime handoffs.`)
}

const (
	codeOK    = 0
	codeUsage = 2
	codeError = 4
)

type env struct {
	stdin          io.Reader
	stdout, stderr io.Writer
}

func defaultState() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "org-state"
	}
	return filepath.Join(home, "dev", "org", "state")
}
func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func printJSON(e *env, v any) error {
	return json.NewEncoder(e.stdout).Encode(v)
}
