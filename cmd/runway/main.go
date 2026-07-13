// runway — local execution-runtime controller. Owns one foreground run until
// a single terminal receipt (result.json + run_terminal). `reconcile` repairs
// controller loss for one known run ID (Flow F).
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/controller"
	"github.com/itsHabib/workbench/cmd/runway/internal/state"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: runway <run|watch|logs|cancel|result|reconcile> ...")
		os.Exit(controller.ExitUsage)
	}
	os.Exit(dispatch(os.Args[1:]))
}

func dispatch(args []string) int {
	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "watch":
		return cmdWatch(args[1:])
	case "logs":
		return cmdLogs(args[1:])
	case "cancel":
		return cmdCancel(args[1:])
	case "result":
		return cmdResult(args[1:])
	case "reconcile":
		return cmdReconcile(args[1:])
	}
	fmt.Fprintf(os.Stderr, "runway: unknown verb %q\n", args[0])
	return controller.ExitUsage
}

func cmdRun(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	spec := fs.String("spec", "", "path to placed request.json")
	bundleDir := fs.String("bundle", "", "work bundle directory")
	stateDir := fs.String("state", state.DefaultRoot(), "runway state root (default $"+state.EnvState+" or ~/.runway)")
	jsonOut := fs.Bool("json", false, "emit result.json on stdout; diagnostics on stderr")
	if err := fs.Parse(args); err != nil {
		return controller.ExitUsage
	}
	if *spec == "" || *bundleDir == "" {
		fmt.Fprintln(os.Stderr, "usage: runway run --spec <request.json> --bundle <dir> [--state <dir>] [--json]")
		return controller.ExitUsage
	}
	stateAbs, err := controller.AbsStateRoot(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return controller.ExitUsage
	}
	out, err := controller.Run(*spec, *bundleDir, stateAbs, controller.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if controller.IsUsage(err) {
			return controller.ExitUsage
		}
		return controller.ExitFailed
	}
	if *jsonOut {
		if err := controller.WriteJSON(os.Stdout, out.Result); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return controller.ExitFailed
		}
	}
	fmt.Fprintf(os.Stderr, "runway: run_id=%s status=%s reason=%s\n", out.RunID, out.Result.Status, out.Result.ReasonCode)
	return out.ExitCode
}

func cmdWatch(args []string) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	stateDir := fs.String("state", state.DefaultRoot(), "runway state root")
	after := fs.Int64("after", 0, "resume after this sequence number")
	follow := fs.Bool("follow", false, "follow until terminal")
	jsonOut := fs.Bool("json", false, "machine output on stdout (default for watch)")
	if err := fs.Parse(args); err != nil {
		return controller.ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: runway watch <run-id> [--state <dir>] [--after <seq>] [--follow] [--json]")
		return controller.ExitUsage
	}
	_ = jsonOut // watch always writes NDJSON events to stdout
	stateAbs, err := controller.AbsStateRoot(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return controller.ExitUsage
	}
	if err := controller.Watch(stateAbs, fs.Arg(0), *after, *follow, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return mapObserveErr(err)
	}
	return controller.ExitOK
}

func cmdLogs(args []string) int {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	stateDir := fs.String("state", state.DefaultRoot(), "runway state root")
	stream := fs.String("stream", "stdout", "stdout|stderr")
	follow := fs.Bool("follow", false, "follow until terminal")
	if err := fs.Parse(args); err != nil {
		return controller.ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: runway logs <run-id> [--state <dir>] [--stream stdout|stderr] [--follow]")
		return controller.ExitUsage
	}
	stateAbs, err := controller.AbsStateRoot(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return controller.ExitUsage
	}
	if err := controller.TailLogs(stateAbs, fs.Arg(0), *stream, *follow, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return mapObserveErr(err)
	}
	return controller.ExitOK
}

func cmdCancel(args []string) int {
	fs := flag.NewFlagSet("cancel", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	stateDir := fs.String("state", state.DefaultRoot(), "runway state root")
	jsonOut := fs.Bool("json", false, "machine output on stdout")
	if err := fs.Parse(args); err != nil {
		return controller.ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: runway cancel <run-id> [--state <dir>] [--json]")
		return controller.ExitUsage
	}
	stateAbs, err := controller.AbsStateRoot(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return controller.ExitUsage
	}
	out, err := controller.RequestCancel(stateAbs, fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return mapObserveErr(err)
	}
	if *jsonOut {
		payload := map[string]any{"noop": out.NoOp}
		if out.Result != nil {
			payload["result"] = out.Result
		}
		_ = controller.WriteJSON(os.Stdout, payload)
	}
	if out.NoOp {
		fmt.Fprintln(os.Stderr, "runway: cancel no-op")
	}
	return controller.ExitOK
}

func cmdResult(args []string) int {
	fs := flag.NewFlagSet("result", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	stateDir := fs.String("state", state.DefaultRoot(), "runway state root")
	wait := fs.Bool("wait", false, "wait for terminal receipt")
	timeoutStr := fs.String("timeout", "", "mandatory with --wait (e.g. 5s)")
	jsonOut := fs.Bool("json", false, "emit receipt on stdout")
	if err := fs.Parse(args); err != nil {
		return controller.ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: runway result <run-id> [--state <dir>] [--wait --timeout <duration>] [--json]")
		return controller.ExitUsage
	}
	if *wait && *timeoutStr == "" {
		fmt.Fprintln(os.Stderr, "runway: --timeout is mandatory with --wait")
		return controller.ExitUsage
	}
	var timeout time.Duration
	if *timeoutStr != "" {
		d, perr := time.ParseDuration(*timeoutStr)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "runway: bad --timeout: %v\n", perr)
			return controller.ExitUsage
		}
		timeout = d
	}
	stateAbs, err := controller.AbsStateRoot(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return controller.ExitUsage
	}
	res, err := controller.ReadResult(stateAbs, fs.Arg(0), *wait, timeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if errors.Is(err, controller.ErrWaitTimeout) {
			return controller.ExitTimedOut
		}
		return mapObserveErr(err)
	}
	if *jsonOut {
		if err := controller.WriteJSON(os.Stdout, res); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return controller.ExitFailed
		}
	} else {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
	}
	return controller.ExitFromResult(res)
}

func cmdReconcile(args []string) int {
	fs := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	stateDir := fs.String("state", state.DefaultRoot(), "runway state root")
	jsonOut := fs.Bool("json", false, "machine output on stdout")
	if err := fs.Parse(args); err != nil {
		return controller.ExitUsage
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: runway reconcile <run-id> [--state <dir>] [--json]")
		return controller.ExitUsage
	}
	stateAbs, err := controller.AbsStateRoot(*stateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return controller.ExitUsage
	}
	out, err := controller.Reconcile(stateAbs, fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return mapObserveErr(err)
	}
	if *jsonOut {
		payload := map[string]any{
			"noop":    out.NoOp,
			"mutated": out.Mutated,
		}
		if out.Result != nil {
			payload["result"] = out.Result
		}
		if out.Owner != nil {
			payload["owner"] = out.Owner
		}
		_ = controller.WriteJSON(os.Stdout, payload)
	}
	if out.NoOp {
		fmt.Fprintln(os.Stderr, "runway: reconcile no-op")
	}
	if out.Result != nil {
		fmt.Fprintf(os.Stderr, "runway: run_id=%s status=%s reason=%s\n",
			out.Result.RunID, out.Result.Status, out.Result.ReasonCode)
	}
	return out.ExitCode
}

func mapObserveErr(err error) int {
	if os.IsNotExist(err) {
		return controller.ExitUsage
	}
	return controller.ExitFailed
}
