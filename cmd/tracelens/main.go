// Command tracelens ingests an agent trace and prints the diagnostic verdict.
// Use -json for machine-readable output.
//
//	tracelens run.jsonl                 a JSONL trace file (or stdin)
//	cat run.jsonl | tracelens -json
//	tracelens ship <run-ref>            a persisted ship run; see ship.go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/itsHabib/workbench/cmd/tracelens/internal/tracelens"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "ship" {
		os.Exit(shipMain(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "eval" {
		os.Exit(evalMain(os.Args[2:]))
	}
	if len(os.Args) > 1 && os.Args[1] == "report" {
		os.Exit(reportMain(os.Args[2:]))
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tracelens:", err)
		os.Exit(1)
	}
}

func run() error {
	asJSON := flag.Bool("json", false, "emit the report as JSON")
	quiet := flag.Bool("quiet", false, "skip the trace listing, print only the verdict")
	flag.Parse()

	src, closeFn, err := input(flag.Args())
	if err != nil {
		return err
	}
	defer closeFn()

	tr, err := tracelens.ParseJSONL(src)
	if err != nil {
		return err
	}
	report := tracelens.Analyze(tr, tracelens.DefaultConfig())

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report.Verdict())
	}
	if !*quiet {
		fmt.Print(tracelens.RenderTrace(tr))
		fmt.Println()
	}
	fmt.Print(tracelens.RenderReport(report))
	return nil
}

// input returns the trace source: the named file, or stdin when no path is given.
func input(args []string) (io.Reader, func(), error) {
	if len(args) == 0 {
		return os.Stdin, func() {}, nil
	}
	f, err := os.Open(args[0])
	if err != nil {
		return nil, func() {}, err
	}
	return f, func() { f.Close() }, nil
}
