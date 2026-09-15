package main

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/evidence"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/plan"
)

// cmdLog prints the retained evidence: what a fresh caller needs to know
// about earlier applies, including attempts that never recorded a result.
func cmdLog(args []string, stdout, stderr io.Writer) int {
	fs, dir := flags("log", stderr)
	if fs.Parse(args) != nil {
		return exitUsage
	}
	ws, err := adapter.NewWorkspace(*dir)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitUsage
	}
	jr, err := evidence.Open(ws.Root())
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitFailed
	}
	entries := jr.Entries()
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "No evidence yet: nothing has been applied here.")
		return exitOK
	}
	fmt.Fprintf(stdout, "Evidence in %s (%d entries; each attempt's started entry is folded into its result):\n", evidence.File, len(entries))
	finished := terminal(entries)
	width := 0
	for _, e := range entries {
		width = max(width, len(e.Address))
	}
	for _, e := range entries {
		if line, ok := logLine(e, finished, width); ok {
			fmt.Fprintln(stdout, line)
		}
	}
	if jr.Unreadable > 0 {
		fmt.Fprintf(stdout, "%d unreadable line(s) ignored (a torn append?)\n", jr.Unreadable)
	}
	return exitOK
}

func attemptKey(e evidence.Entry) string { return fmt.Sprintf("%d %s", e.Run, e.Address) }

func terminal(entries []evidence.Entry) map[string]bool {
	done := map[string]bool{}
	for _, e := range entries {
		if e.Event != evidence.Started {
			done[attemptKey(e)] = true
		}
	}
	return done
}

func logLine(e evidence.Entry, finished map[string]bool, width int) (string, bool) {
	head := fmt.Sprintf("  run %d  %-*s  %-6s", e.Run, width, e.Address, e.Action)
	switch e.Event {
	case evidence.Started:
		if finished[attemptKey(e)] {
			return "", false
		}
		return head + "  INTERRUPTED  started, no result recorded", true
	case evidence.Failed:
		return head + "  FAILED       " + e.Error, true
	}
	return head + "  succeeded    " + receiptText(e), true
}

func receiptText(e evidence.Entry) string {
	if e.Fact != "" {
		return plan.Short(e.Fact)
	}
	var parts []string
	for _, p := range sorted(e.Inputs) {
		parts = append(parts, "in "+p+" "+plan.Short(e.Inputs[p]))
	}
	for _, p := range sorted(e.Outputs) {
		parts = append(parts, "out "+p+" "+plan.Short(e.Outputs[p]))
	}
	return strings.Join(parts, ", ")
}

func sorted(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
