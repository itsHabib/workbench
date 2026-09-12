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

	"github.com/itsHabib/workbench/cmd/console/internal/fleetcli"
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
                     [-fleet PATH] [-fleet-state DIR] [-tracelens PATH]
  A local, read-only web view of gate's inbox — parked runs + the grant ledger,
  with a click-through to any run's trace. It shells the gate binary for data;
  judging and minting stay in the CLI. -state defaults to $GATE_STATE, -gate to
  the "gate" binary on PATH. /fleet shows local agent activity; -fleet and
  -tracelens default to those binaries on PATH, -fleet-state to $FLEET_STATE.`)
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:7788", "loopback address to serve on (must be localhost/loopback)")
	state := fs.String("state", os.Getenv("GATE_STATE"), "gate state dir, passed through to the gate binary [env GATE_STATE]")
	gateBin := fs.String("gate", "gate", "path to the gate binary")
	fleetBin := fs.String("fleet", "fleet", "path to the Fleet binary")
	lensBin := fs.String("tracelens", "tracelens", "path to TraceLens binary")
	fleetState := fs.String("fleet-state", os.Getenv("FLEET_STATE"), "Fleet state root")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client := gatecli.New(*gateBin, *state, nil)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return web.Serve(ctx, *addr, client, func(bound string) {
		fmt.Printf("console: http://%s  (gate=%s state=%s)\n", bound, *gateBin, orDefault(*state))
		fmt.Println("console: read-only — judging and minting stay in the CLI. Ctrl-C to stop.")
	}, fleetcli.New(*fleetBin, *lensBin, *fleetState, nil))
}

func orDefault(s string) string {
	if s == "" {
		return "$GATE_STATE / gate default"
	}
	return s
}
