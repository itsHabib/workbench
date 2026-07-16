package tracelens

import (
	"fmt"
	"sort"
	"strings"
)

// RenderEvaluation formats the corpus result for an operator. Undefined
// metrics render as n/a rather than a misleading zero.
func RenderEvaluation(e Evaluation) string {
	var b strings.Builder
	status := "PASS"
	if !e.Pass {
		status = "FAIL"
	}
	fmt.Fprintf(&b, "EVALUATION %s · %d cases · macro precision %s\n", status, len(e.Cases), metricText(e.MacroPrecision))
	kinds := make([]string, 0, len(e.Metrics))
	for kind := range e.Metrics {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	for _, kind := range kinds {
		m := e.Metrics[kind]
		fmt.Fprintf(&b, "  %-14s TP %-2d FP %-2d FN %-2d precision %-6s recall %s\n", kind, m.TruePositive, m.FalsePositive, m.FalseNegative, metricText(m.Precision), metricText(m.Recall))
	}
	for _, failure := range e.Failures {
		fmt.Fprintf(&b, "  FAIL %s\n", failure)
	}
	return b.String()
}

func metricText(v *float64) string {
	if v == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%%", *v*100)
}
