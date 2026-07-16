package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/itsHabib/workbench/contracts"

	"github.com/itsHabib/workbench/cmd/tracelens/internal/tracelens"
)

// shipMain handles `tracelens ship [-json] [-quiet] <run-ref>`: analyze a
// persisted ship run and gate on the verdict. The run ref is a workflow-run id
// (resolved under the ship runs dir), a run directory, or an events file path.
// Exit codes: 0 pass or escalate, 1 block, 2 error.
func shipMain(argv []string) int {
	fs := flag.NewFlagSet("tracelens ship", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "emit the report as JSON")
	quiet := fs.Bool("quiet", false, "skip the trace listing, print only the verdict")
	if err := fs.Parse(argv); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: tracelens ship [-json] [-quiet] <run-ref>")
		return 2
	}
	code, err := runShip(fs.Arg(0), *asJSON, *quiet)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tracelens:", err)
		return 2
	}
	return code
}

func runShip(ref string, asJSON, quiet bool) (int, error) {
	eventsPath, err := resolveRunRef(ref)
	if err != nil {
		return 0, err
	}
	f, err := os.Open(eventsPath)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	tr, err := tracelens.ParseShipEvents(f)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", eventsPath, err)
	}
	report := tracelens.Analyze(tr, tracelens.DefaultConfig())
	if err := printShipReport(filepath.Dir(eventsPath), tr, report, asJSON, quiet); err != nil {
		return 0, err
	}
	return gateCode(report.Verdict()), nil
}

// gateCode maps a verdict to the subcommand's exit code: only a block trips the
// gate. Escalate and pass both exit 0 — the same behavior as the old
// pathological/degraded/healthy mapping, rekeyed onto the decision axis.
func gateCode(v contracts.Verdict) int {
	if v.Decision == contracts.DecisionBlock {
		return 1
	}
	return 0
}

func printShipReport(runDir string, tr tracelens.Trajectory, r tracelens.Report, asJSON, quiet bool) error {
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r.Verdict())
	}
	if line := runContextLine(runDir); line != "" {
		fmt.Println(line)
	}
	if !quiet {
		fmt.Print(tracelens.RenderTrace(tr))
		fmt.Println()
	}
	fmt.Print(tracelens.RenderReport(r))
	return nil
}

// shipResult is the slice of a run's result.json the context line needs.
type shipResult struct {
	Status          string `json:"status"`
	DurationMS      int64  `json:"durationMs"`
	FailureCategory string `json:"failureCategory"`
	FailureDetail   string `json:"failureDetail"`
}

// runContextLine summarizes the run's own terminal record when the events file
// sits in a run directory with a result.json; "" when unavailable.
func runContextLine(dir string) string {
	raw, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if err != nil {
		return ""
	}
	var res shipResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return ""
	}
	if res.Status == "" {
		return ""
	}
	line := fmt.Sprintf("RUN    %s · %s", res.Status, time.Duration(res.DurationMS)*time.Millisecond)
	if res.FailureCategory != "" {
		line += " · " + res.FailureCategory
	}
	if res.FailureDetail != "" {
		line += " (" + res.FailureDetail + ")"
	}
	return line
}

// resolveRunRef turns a run reference into the events file path to read: a
// direct file path, a run directory, or a workflow-run id resolved under the
// ship runs dir.
func resolveRunRef(ref string) (string, error) {
	info, err := os.Stat(ref)
	if err == nil && info.IsDir() {
		return filepath.Join(ref, "events.ndjson"), nil
	}
	if err == nil {
		return ref, nil
	}
	if !strings.HasPrefix(ref, "wf_") {
		return "", fmt.Errorf("run ref %q is neither an existing path nor a workflow-run id", ref)
	}
	return filepath.Join(shipRunsDir(), ref, "events.ndjson"), nil
}

// shipRunsDir mirrors ship's own runs-dir resolution: SHIP_RUNS_DIR when set,
// else <config-home>/ship/runs.
func shipRunsDir() string {
	if dir := os.Getenv("SHIP_RUNS_DIR"); dir != "" {
		return dir
	}
	return filepath.Join(configHome(), "ship", "runs")
}

// configHome matches ship's entrypoints: %APPDATA% on Windows (falling back
// to ~/AppData/Roaming), else an absolute XDG_CONFIG_HOME, else ~/.config —
// including on macOS, where ship deliberately uses ~/.config too.
func configHome() string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return appData
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, "AppData", "Roaming")
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" && filepath.IsAbs(xdg) {
		return xdg
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config")
}
