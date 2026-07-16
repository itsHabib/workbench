package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/itsHabib/workbench/cmd/tracelens/internal/tracelens"
)

// evalMain runs the checked-in corpus gate. Exit codes: 0 pass, 1 metric or
// label gate failure, 2 input/infrastructure error.
func evalMain(argv []string) int {
	fs := flag.NewFlagSet("tracelens eval", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit the evaluation as JSON")
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: tracelens eval [-json] <manifest-or-corpus-dir>")
		return 2
	}
	evaluation, err := tracelens.EvaluateCorpus(fs.Arg(0), tracelens.DefaultConfig())
	if err != nil {
		fmt.Fprintln(os.Stderr, "tracelens:", err)
		return 2
	}
	if err := printEvaluation(evaluation, *asJSON); err != nil {
		fmt.Fprintln(os.Stderr, "tracelens:", err)
		return 2
	}
	if !evaluation.Pass {
		return 1
	}
	return 0
}

// printEvaluation renders the evaluation to stdout, machine- or human-shaped.
func printEvaluation(evaluation tracelens.Evaluation, asJSON bool) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(evaluation)
	}
	fmt.Print(tracelens.RenderEvaluation(evaluation))
	return nil
}
