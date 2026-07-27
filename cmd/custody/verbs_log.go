package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/itsHabib/workbench/cmd/custody/internal/rollup"
)

// cmdLog dispatches the `log` verb. v0 carries one subcommand, `rollup`, which
// aggregates the request-log artifact into a deterministic per-key summary —
// the telemetry base a human or a local anomaly reviewer reads instead of the
// raw JSONL. Read-side only: it never touches grants, secrets, or the manifest.
func cmdLog(args []string) error {
	if len(args) == 0 || isHelp(args[0]) {
		fmt.Fprintln(os.Stderr, "usage: custody log rollup [-state <dir>] [-since <dur>] [-json]")
		return nil
	}
	if args[0] != "rollup" {
		return fmt.Errorf("log: unknown subcommand %q (want: rollup)", args[0])
	}
	fs := flag.NewFlagSet("log rollup", flag.ContinueOnError)
	stateDir := fs.String("state", envOr("CUSTODY_STATE", defaultStateDir()), "state dir holding log/requests.jsonl [env CUSTODY_STATE]")
	since := fs.Duration("since", 0, "keep only records within this duration of the NEWEST record (0 = all); log-anchored, not wall-clock")
	asJSON := fs.Bool("json", false, "emit the rollup as indented JSON (the artifact form)")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return errUsageShown
	}
	path := filepath.Join(*stateDir, "log", "requests.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("log rollup: open %s (has `custody serve` handled a request yet?): %w", path, err)
	}
	defer f.Close()
	sum, err := rollup.Aggregate(f, *since)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(sum)
	}
	renderRollup(os.Stdout, sum)
	return nil
}

// renderRollup prints the human view of a rollup: one header line, the verdict
// mix, then a block per key in sorted order.
func renderRollup(w io.Writer, sum *rollup.Summary) {
	fmt.Fprintf(w, "custody log rollup — %d requests", sum.Total)
	if sum.Total > 0 {
		fmt.Fprintf(w, "  %s → %s", sum.Window.From, sum.Window.To)
	}
	fmt.Fprintln(w)
	if sum.Malformed > 0 || sum.UnsupportedSchema > 0 {
		fmt.Fprintf(w, "  skipped: %d malformed, %d unsupported-schema lines\n", sum.Malformed, sum.UnsupportedSchema)
	}
	fmt.Fprintf(w, "  verdicts: %s\n", countLine(sum.ByVerdict))
	for _, key := range sortedKeys(sum.Keys) {
		renderKey(w, key, sum.Keys[key])
	}
}

// renderKey prints one key's block.
func renderKey(w io.Writer, key string, ks *rollup.KeySummary) {
	fmt.Fprintf(w, "  %s: %d req · %s · grants %d · p50 %dms p95 %dms max %dms\n",
		key, ks.Total, countLine(ks.ByVerdict), ks.DistinctGrants,
		ks.Latency.P50, ks.Latency.P95, ks.Latency.Max)
	if len(ks.UpstreamStatus) > 0 {
		fmt.Fprintf(w, "    upstream: %s\n", countLine(ks.UpstreamStatus))
	}
	for _, t := range ks.DeniedTargets {
		fmt.Fprintf(w, "    denied ×%d  %s\n", t.Count, t.Target)
	}
	if len(ks.DeniedQueryKeys) > 0 {
		fmt.Fprintf(w, "    denied query keys: %s\n", countLine(ks.DeniedQueryKeys))
	}
}

// countLine renders a count map as "a 3 · b 1" in sorted-key order.
func countLine(counts map[string]int) string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Strings(names)
	line := ""
	for i, name := range names {
		if i > 0 {
			line += " · "
		}
		line += fmt.Sprintf("%s %d", name, counts[name])
	}
	return line
}

// sortedKeys returns the key names in lexicographic order for stable output.
func sortedKeys(keys map[string]*rollup.KeySummary) []string {
	names := make([]string, 0, len(keys))
	for name := range keys {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
