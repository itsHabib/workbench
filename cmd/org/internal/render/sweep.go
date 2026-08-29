package render

import (
	"fmt"
	"strings"

	"github.com/itsHabib/workbench/cmd/org/internal/survey"
)

// Sweep renders a continuity survey: one row per role, then the two rates the
// substrate is a bet on.
//
// A rate with no data renders as "—", never as 0%. The difference between "no
// session has ended yet" and "every session ended without distilling" is the
// whole finding, and a renderer that collapses them reports a failure that did
// not happen.
func Sweep(roles []survey.Role, t survey.Totals, conflicts []survey.AssignConflict, drifts []survey.ScopeDrift) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%-8s  %-32s  %-9s  %5s  %5s  %5s  %9s  %8s  %s\n",
		"TENANT", "ROLE", "PHASE", "RECS", "INCS", "CLMS", "ORPH/DISCH", "CKPT/MRK", "FLAGS")
	for _, r := range roles {
		fmt.Fprintf(&sb, "%-8s  %-32s  %-9s  %5d  %5d  %5d  %9s  %8s  %s\n",
			r.Tenant, r.Role, r.Phase, r.Records, r.Incarnations, r.Claims,
			fmt.Sprintf("%d/%d", r.Orphaned, r.Discharged),
			fmt.Sprintf("%d/%d", r.Checkpoints, r.Marks),
			flags(r))
	}
	if len(conflicts) > 0 {
		fmt.Fprintln(&sb, "\nassign_conflicts (detected, not prevented):")
		for _, conflict := range conflicts {
			fmt.Fprintf(&sb, "  %s  %s  %s\n",
				conflict.Tenant, conflict.Work, strings.Join(conflict.Roles, ", "))
		}
	}
	if len(drifts) > 0 {
		fmt.Fprintln(&sb, "\nscope_drift (held outside the charter's own scope):")
		for _, drift := range drifts {
			fmt.Fprintf(&sb, "  %s  %s  holds %s  scope %s\n",
				drift.Tenant, drift.Role, drift.Work, strings.Join(drift.Scope, ", "))
		}
	}
	fmt.Fprintf(&sb, "\n%d role(s) · %d records · %d incarnation(s) · %d claim(s), %d closed\n",
		t.Roles, t.Records, t.Incarnations, t.Claims, t.Terminals)
	fmt.Fprintf(&sb, "distilled session ends: %s (%d checkpoint(s) of %d end(s))\n",
		rate(t.DistillRate), t.Checkpoints, t.Checkpoints+t.Marks)
	fmt.Fprintf(&sb, "inherited obligations discharged: %s (%d of %d orphaned)\n",
		rate(t.DischargeRate), t.Discharged, t.Orphaned)
	if line := attention(t, len(conflicts), len(drifts)); line != "" {
		fmt.Fprintf(&sb, "attention: %s\n", line)
	}
	return sb.String()
}

// attention composes the closing line from the findings that actually fired.
// A zero count is never rendered: "attention: 0 dangling" reads as a checked
// and cleared state, which is exactly what a finding-free sweep is not saying.
func attention(t survey.Totals, conflicts, drifts int) string {
	var parts []string
	if t.Dangling+t.Late+t.Broken > 0 {
		parts = append(parts, fmt.Sprintf("%d dangling · %d late · %d chain(s) that do not fold",
			t.Dangling, t.Late, t.Broken))
	}
	if conflicts > 0 {
		parts = append(parts, fmt.Sprintf("%d assign_conflict(s)", conflicts))
	}
	if drifts > 0 {
		parts = append(parts, fmt.Sprintf("%d scope_drift(s)", drifts))
	}
	return strings.Join(parts, " · ")
}

// flags renders a role's attention markers, most serious first.
func flags(r survey.Role) string {
	var out []string
	if r.Err != "" {
		out = append(out, "BROKEN")
	}
	if r.Dangling != "" {
		out = append(out, "dangling:"+r.Dangling)
	}
	if r.Late {
		out = append(out, "LATE")
	}
	if r.Degraded {
		out = append(out, "degraded")
	}
	if r.OpenIntents > 0 {
		out = append(out, fmt.Sprintf("intents:%d", r.OpenIntents))
	}
	if r.OpenEscalations > 0 {
		out = append(out, fmt.Sprintf("escalations:%d", r.OpenEscalations))
	}
	if len(out) == 0 {
		return "ok"
	}
	return strings.Join(out, " ")
}

// rate renders a ratio, or an em dash when there is no data to divide.
func rate(v float64) string {
	if v < 0 {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", v*100)
}
