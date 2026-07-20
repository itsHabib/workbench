package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/itsHabib/workbench/driverstate"
)

// cmdState prints the reduced RunState for a run.
func cmdState(dir string, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("state", flag.ContinueOnError)
	run := fs.String("run", "", "run id")
	asJSON := fs.Bool("json", false, "emit RunState as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *run == "" {
		return fmt.Errorf("state: --run is required")
	}
	state, err := driverstate.Reduce(dir, *run)
	if err != nil {
		return err
	}
	if *asJSON {
		return encodeJSON(stdout, state)
	}
	fmt.Fprintf(stdout, "run %s: %s (repo %s, %d streams)\n", *run, state.Run.Status, state.Run.Repo, len(state.Streams))
	return nil
}

// cmdRuns lists run summaries with the repo and live filters, matching the
// driver_runs verb 1:1.
func cmdRuns(dir string, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("runs", flag.ContinueOnError)
	repo := fs.String("repo", "", "filter to a repo")
	live := fs.Bool("live", false, "only unfinished (open) runs")
	parent := fs.String("parent", "", "filter to the children of a parent run")
	asJSON := fs.Bool("json", false, "emit the summaries as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	all, err := driverstate.Runs(dir)
	if err != nil {
		return err
	}
	out := filterRuns(all, *repo, *live, *parent)
	if *asJSON {
		return encodeJSON(stdout, out)
	}
	for _, r := range out {
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", r.Run, r.Status, r.Repo)
	}
	return nil
}

// filterRuns applies the repo and live filters. live keeps only open runs — the
// resumable set (spec §7 F3). Returns a non-nil slice so --json emits [] not null.
func filterRuns(all []driverstate.RunSummary, repo string, live bool, parent string) []driverstate.RunSummary {
	out := make([]driverstate.RunSummary, 0, len(all))
	for _, r := range all {
		if repo != "" && r.Repo != repo {
			continue
		}
		if live && r.Status != driverstate.RunStatusOpen {
			continue
		}
		if parent != "" && r.Parent != parent {
			continue
		}
		out = append(out, r)
	}
	return out
}

// cmdRollup is the CLI mirror of driver_rollup: it joins a parent run to its
// child sub-runs and prints the per-stream roster (or the JSON ParentRollup).
func cmdRollup(dir string, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("rollup", flag.ContinueOnError)
	run := fs.String("run", "", "the parent run id")
	asJSON := fs.Bool("json", false, "emit the ParentRollup as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *run == "" {
		return fmt.Errorf("rollup: --run is required")
	}
	r, err := driverstate.Rollup(dir, *run)
	if err != nil {
		return err
	}
	if *asJSON {
		return encodeJSON(stdout, r)
	}
	fmt.Fprintf(stdout, "%s\tboundary=%s\treached=%t\n", r.Run, r.DoneBoundary, r.BoundaryReached)
	for _, s := range r.Streams {
		fmt.Fprintf(stdout, "  %s\tparent=%s\tchild=%s(%s)\tpr=%d\tagrees=%t\tfriction=cycles:%d/retries:%d/conflict:%t\n",
			s.Stream, s.ParentStatus, s.ChildRun, s.ChildStatus, s.PR, s.Agrees,
			s.Friction.GateCycles, s.Friction.Retries, s.Friction.WorktreeConflict)
	}
	return nil
}

// cmdVerify checks a run's hash chain. A broken chain returns the error (exit 1);
// --json emits {run, ok} on success.
func cmdVerify(dir string, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	run := fs.String("run", "", "run id")
	asJSON := fs.Bool("json", false, "emit {run, ok} as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *run == "" {
		return fmt.Errorf("verify: --run is required")
	}
	if err := driverstate.Verify(dir, *run); err != nil {
		return err
	}
	if *asJSON {
		return encodeJSON(stdout, map[string]any{"run": *run, "ok": true})
	}
	fmt.Fprintf(stdout, "run %s: chain ok\n", *run)
	return nil
}

// encodeJSON writes v as indented JSON with a trailing newline — the shared
// --json renderer for every read verb.
func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
