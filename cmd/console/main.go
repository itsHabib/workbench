// Command console is a local, read-only web view of gate's inbox: the runs
// parked for judgment and the grant ledger, plus a click-through to any run's
// decision trace. It is a pure renderer over gate's own JSON — it shells the
// gate binary (`gate next -json`, `gate explain -json`, `gate audit`) and never
// reads gate's state or imports its decision code. There are no action
// endpoints in this version: judging and minting stay in the CLI.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/itsHabib/workbench/cmd/console/internal/gatecli"
	"github.com/itsHabib/workbench/cmd/console/internal/web"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = cmdServe(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "console:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: console serve [-addr 127.0.0.1:7788] [-state DIR] [-gate PATH]
  A local, read-only web view of gate's inbox — parked runs + the grant ledger,
  with a click-through to any run's trace. It shells the gate binary for data;
  judging and minting stay in the CLI. -state defaults to $GATE_STATE, -gate to
  the "gate" binary on PATH.`)
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:7788", "loopback address to serve on (must be localhost/loopback)")
	state := fs.String("state", os.Getenv("GATE_STATE"), "gate state dir, passed through to the gate binary [env GATE_STATE]")
	gateBin := fs.String("gate", "gate", "path to the gate binary")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client := gatecli.New(*gateBin, *state, nil)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return web.Serve(ctx, *addr, client, func(bound string) {
		fmt.Printf("console: http://%s  (gate=%s state=%s)\n", bound, *gateBin, orDefault(*state))
		fmt.Println("console: read-only — judging and minting stay in the CLI. Ctrl-C to stop.")
	})
}

func orDefault(s string) string {
	if s == "" {
		return "$GATE_STATE / gate default"
	}
	return s
}
