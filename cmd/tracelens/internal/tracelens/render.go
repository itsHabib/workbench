package tracelens

import (
	"fmt"
	"strconv"
	"strings"
)

// RenderTrace formats a trajectory as a compact, human-readable step list.
func RenderTrace(t Trajectory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "TRACE  %d steps  $%.4f total\n", len(t.Steps), t.TotalCost())
	for _, s := range t.Steps {
		b.WriteString("  " + renderStep(s) + "\n")
	}
	return b.String()
}

func renderStep(s Step) string {
	tag := fmt.Sprintf("#%-2d", s.Index)
	if !s.IsTool() {
		th := short(s.Thought, 62)
		if th == "" {
			th = "(" + s.Role + ")"
		}
		return tag + " · " + th
	}
	return fmt.Sprintf("%s %-46s %s  $%.4f", tag, short(s.callSig(), 46), statusTag(s), s.CostUSD)
}

func statusTag(s Step) string {
	if s.Failed() {
		return "FAIL"
	}
	if s.OK == nil {
		return " …  "
	}
	return " ok "
}

// RenderReport formats the verdict: the decision/tier banner, headline, totals,
// then each ranked finding with its evidence steps and its repair suggestion.
func RenderReport(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "VERDICT  %s · %s\n", strings.ToUpper(r.Decision), r.Tier)
	fmt.Fprintf(&b, "  %s\n", r.Headline)
	fmt.Fprintf(&b, "  %d steps · $%.4f spent · $%.4f wasted · %d findings\n",
		r.Steps, r.TotalUSD, r.WastedUSD, len(r.Findings))
	if len(r.Findings) == 0 {
		return b.String()
	}
	b.WriteString("\nFINDINGS\n")
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "  [%-8s] %s\n", f.Severity, f.Summary)
		fmt.Fprintf(&b, "             steps: %s\n", joinInts(f.Steps))
		fmt.Fprintf(&b, "             fix:   %s\n", f.Repair)
	}
	return b.String()
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ",")
}
