// Command org registers editable role cards. A role is configuration, not a
// session, a work claim, a mailbox, or an authority grant.
package main

import (
	"fmt"
	"io"
	"os"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return codeUsage
	}
	if args[0] == "legacy" {
		return runLegacy(args[1:], stdin, stdout, stderr)
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
		fmt.Fprintf(stderr, "org: %q is not a role-card command. Work, mail and handoffs belong to their runtime. For an existing Baton chain use org legacy %s.\n", args[0], args[0])
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
Existing journal records and recovery commands remain under org legacy <verb>.`)
}
