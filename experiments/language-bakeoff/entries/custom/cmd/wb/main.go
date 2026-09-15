// Command wb plans and applies files written in the wb language.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/adapters/command"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/adapters/dir"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/adapters/file"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/cli"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/kind"
)

func main() {
	// The registry is the extension point: a new kind is one adapter
	// package and one entry here. The parser and planner never change.
	reg, err := kind.NewRegistry(
		[]kind.Resource{file.Adapter{}, dir.Adapter{}},
		[]kind.Work{command.Adapter{}},
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "wb:", err)
		os.Exit(cli.ExitFailed)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cli.Run(ctx, os.Args[1:], reg, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
