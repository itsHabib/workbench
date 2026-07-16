package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/itsHabib/workbench/cmd/tracelens/internal/tracelens"
)

func reportMain(argv []string) int {
	fs := flag.NewFlagSet("tracelens report", flag.ContinueOnError)
	dialect := fs.String("dialect", string(tracelens.DialectNeutral), "input dialect: neutral-jsonl, ship-cursor, ship-claude, or ship-codex")
	output := fs.String("output", "", "write Markdown to this path (default stdout)")
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: tracelens report [-dialect <dialect>] [-output path] <trace>")
		return 2
	}
	source := fs.Arg(0)
	decoded, err := decodeReportInput(source, tracelens.Dialect(*dialect))
	if err != nil {
		fmt.Fprintln(os.Stderr, "tracelens:", err)
		return 2
	}
	report := tracelens.Analyze(decoded.Trajectory, tracelens.DefaultConfig())
	command := fmt.Sprintf("tracelens report -dialect %s %s", decoded.Dialect, filepath.ToSlash(source))
	markdown := tracelens.RenderMarkdownReport(decoded, report, tracelens.MarkdownReportOptions{Source: filepath.ToSlash(source), GeneratedBy: command})
	if *output == "" {
		fmt.Print(markdown)
		return 0
	}
	if err := os.WriteFile(*output, []byte(markdown), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "tracelens:", err)
		return 2
	}
	return 0
}

func decodeReportInput(path string, dialect tracelens.Dialect) (tracelens.DecodedTrace, error) {
	f, err := os.Open(path)
	if err != nil {
		return tracelens.DecodedTrace{}, err
	}
	defer f.Close()
	return tracelens.DecodeTrace(f, dialect)
}
