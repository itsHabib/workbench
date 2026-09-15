package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/engine"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/wb"
)

var symbols = map[engine.Op]string{
	engine.None: "=", engine.Create: "+", engine.Update: "~", engine.Replace: "-/+", engine.Run: ">",
}

var past = map[engine.Op]string{
	engine.Create: "created", engine.Update: "updated", engine.Replace: "replaced", engine.Run: "ran",
}

func renderPlan(w io.Writer, p engine.Plan) {
	fmt.Fprintf(w, "Plan for %s (intent %s, evidence through #%d)\n\n",
		p.Intent.Workflow, engine.Short(p.IntentDigest), p.EvidenceHead)
	width := addressWidth(p.Steps)
	for _, s := range p.Steps {
		verb, detail := headline(s)
		fmt.Fprintf(w, "  %-3s %-*s  %-10s  %s\n", symbols[s.Op], width, s.Address, verb, detail)
		for _, line := range details(s) {
			fmt.Fprintf(w, "        %s\n", line)
		}
	}
	for _, msg := range p.Warnings {
		fmt.Fprintf(w, "warning: %s\n", msg)
	}
	fmt.Fprintf(w, "\n%s\n", summary(p.Steps))
}

func headline(s engine.Step) (verb, detail string) {
	if s.Op != engine.None {
		return string(s.Op), s.Owns
	}
	if s.Class == wb.Task {
		return "up to date", fmt.Sprintf("%s (run #%d)", s.Owns, s.LastRun)
	}
	return "unchanged", s.Owns
}

func details(s engine.Step) []string {
	switch s.Op {
	case engine.Create, engine.Update:
		return contentDiff(s)
	case engine.Replace:
		return withNote([]string{fmt.Sprintf("%s -> %s", text(s.Before), text(s.After))}, s.Note)
	case engine.Run:
		lines := make([]string, 0, len(s.Reasons))
		for _, r := range s.Reasons {
			lines = append(lines, "why: "+r)
		}
		return lines
	}
	return nil
}

// contentDiff shows a line diff when the adapter provided text, and the
// digests otherwise.
func contentDiff(s engine.Step) []string {
	lines := lineDiff(s.Before.Text, s.After.Text)
	if len(lines) == 0 {
		lines = []string{fmt.Sprintf("%s -> %s", engine.Describe(s.Before), engine.Describe(s.After))}
	}
	return withNote(lines, s.Note)
}

func withNote(lines []string, note string) []string {
	if note == "" {
		return lines
	}
	return append(lines, "("+note+")")
}

func text(st engine.State) string {
	if st.Text != "" {
		return st.Text
	}
	return engine.Describe(st)
}

func summary(steps []engine.Step) string {
	counts := map[engine.Op]int{}
	for _, s := range steps {
		counts[s.Op]++
	}
	pending := len(steps) - counts[engine.None]
	if pending == 0 {
		return fmt.Sprintf("No changes. All %d nodes match the intent.", len(steps))
	}
	var parts []string
	for _, op := range []engine.Op{engine.Create, engine.Update, engine.Replace, engine.Run} {
		if counts[op] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[op], op))
		}
	}
	return fmt.Sprintf("Plan: %d to change (%s), %d unchanged.", pending, strings.Join(parts, ", "), counts[engine.None])
}

func renderResults(w io.Writer, results []engine.Result) {
	steps := make([]engine.Step, 0, len(results))
	for _, r := range results {
		steps = append(steps, r.Step)
	}
	width := addressWidth(steps)
	fmt.Fprintln(w, "Apply:")
	for _, r := range results {
		if r.Status == engine.Unchanged {
			continue
		}
		fmt.Fprintf(w, "  %-3s %-*s  %s\n", symbols[r.Step.Op], width, r.Step.Address, outcome(r))
	}
	done, failed, skipped := count(results, engine.Done), count(results, engine.Failed), count(results, engine.Skipped)
	switch {
	case done+failed+skipped == 0:
		fmt.Fprintln(w, "  nothing to do")
	case failed+skipped == 0:
		fmt.Fprintf(w, "Apply complete: %d done.\n", done)
	default:
		fmt.Fprintf(w, "Apply incomplete: %d done, %d failed, %d skipped. Completed effects are kept; nothing was rolled back.\n",
			done, failed, skipped)
	}
}

func outcome(r engine.Result) string {
	switch r.Status {
	case engine.Done:
		return fmt.Sprintf("%-10s  %s is now %s%s", past[r.Step.Op], r.Step.Owns, engine.Describe(r.Observed), evidence(r.Evidence))
	case engine.Failed:
		return fmt.Sprintf("%-10s  %s%s", "FAILED", r.Detail, evidence(r.Evidence))
	}
	return fmt.Sprintf("%-10s  %s", "skipped", r.Detail)
}

func evidence(seq int) string {
	if seq == 0 {
		return ""
	}
	return fmt.Sprintf(" (evidence #%d)", seq)
}

func renderObserved(w io.Writer, p engine.Plan) {
	pending := p.Pending()
	if len(pending) == 0 {
		fmt.Fprintln(w, "Observed after apply: converged; a new plan proposes no changes.")
		return
	}
	names := make([]string, 0, len(pending))
	for _, s := range pending {
		names = append(names, string(s.Address))
	}
	fmt.Fprintf(w, "Observed after apply: %d still pending (%s). Plan again to review; apply again to retry.\n",
		len(pending), strings.Join(names, ", "))
}

func renderStale(w io.Writer, e *engine.StaleError) {
	fmt.Fprintln(w, "Refusing to apply: the plan is stale. The workspace changed after it was planned:")
	for _, d := range e.Drift {
		fmt.Fprintf(w, "  %s\n", d)
	}
	fmt.Fprintln(w, "No effects were performed. Run plan again to review the current changes.")
}

func addressWidth(steps []engine.Step) int {
	width := 0
	for _, s := range steps {
		width = max(width, len(s.Address))
	}
	return width
}
