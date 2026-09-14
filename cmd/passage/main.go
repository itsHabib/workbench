// Passage checks evidence at handoffs while leaving work within phases open.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/itsHabib/workbench/cmd/passage/internal/passage"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: passage init|status|check|history|record|advance|reopen --work FILE [flags]")
		return 2
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	f.SetOutput(stderr)
	work := f.String("work", "", "work record path (keep outside the Git worktree)")
	root := f.String("root", ".", "input root for init")
	contract := f.String("contract", "", "contract JSON for init")
	expect := f.Int("expect", -1, "observed revision, required for mutations")
	by := f.String("by", "", "identity responsible for the judgment")
	note := f.String("note", "", "reason for this action")
	requirement := f.String("requirement", "", "requirement being judged")
	verdict := f.String("verdict", "", "pass or fail")
	evidence := f.String("evidence", "", "UTF-8 evidence file to retain")
	phase := f.String("phase", "", "earlier phase to reopen")
	if err := f.Parse(args[1:]); err != nil {
		return 2
	}
	if *work == "" || f.NArg() != 0 {
		fmt.Fprintln(stderr, "--work is required; positional arguments are not accepted")
		return 2
	}
	err := execute(args[0], *work, *root, *contract, passage.Change{Action: args[0], Expect: *expect, By: *by, Note: *note, Requirement: *requirement, Verdict: *verdict, Evidence: *evidence, Phase: *phase}, out)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func execute(action, work, root, contract string, change passage.Change, out io.Writer) error {
	if action == "init" {
		return passage.Init(work, root, contract)
	}
	switch action {
	case "record", "advance", "reopen":
		if change.Expect < 0 {
			return fmt.Errorf("--expect revision is required; read status first")
		}
		return passage.Update(work, change)
	case "status", "check", "history":
		r, err := passage.Read(work)
		if err != nil {
			return err
		}
		if action == "history" {
			return emit(out, r)
		}
		s, err := passage.Inspect(r)
		if err != nil {
			return err
		}
		if err := emit(out, s); err != nil {
			return err
		}
		if action == "check" && !s.Ready {
			return fmt.Errorf("handoff needs evidence; see problems")
		}
		return nil
	default:
		return fmt.Errorf("unknown command %q", action)
	}
}

func emit(out io.Writer, value any) error {
	e := json.NewEncoder(out)
	e.SetIndent("", "  ")
	return e.Encode(value)
}
