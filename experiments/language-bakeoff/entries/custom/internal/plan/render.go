package plan

import (
	"fmt"
	"io"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/kind"
)

var symbols = map[string]string{
	string(kind.Create):   "+",
	string(kind.Update):   "~",
	string(kind.OK):       "=",
	string(kind.Conflict): "!",
	RunNow:                ">",
	RunIf:                 "?",
	Skip:                  "=",
}

// Render writes the plan for people: one line per declaration, followed by
// the reasons and any changed lines, then a tally.
func (p *Plan) Render(w io.Writer) {
	fmt.Fprintf(w, "plan for %s:\n", p.Source)
	wl, wk, wi := widths(p.Changes)
	pad := strings.Repeat(" ", 4+wl+1+wk+1+wi+2)
	for _, ch := range p.Changes {
		fmt.Fprintf(w, "  %s %-*s %-*s %-*s  %s\n", symbols[ch.Action], wl, ch.Lifecycle, wk, ch.Kind, wi, ch.ID, ch.Summary)
		for _, r := range ch.Reasons {
			fmt.Fprintf(w, "%s* %s\n", pad, r)
		}
		for _, l := range ch.Detail {
			fmt.Fprintf(w, "%s| %s\n", pad, l)
		}
	}
	for _, n := range p.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
	fmt.Fprintf(w, "plan: %s\n", p.tally())
}

func widths(chs []Change) (life, kind, id int) {
	for _, ch := range chs {
		life, kind, id = max(life, len(ch.Lifecycle)), max(kind, len(ch.Kind)), max(id, len(ch.ID))
	}
	return life, kind, id
}

// Pending reports whether applying the plan would do anything.
func (p *Plan) Pending() bool {
	for _, ch := range p.Changes {
		if ch.Action != string(kind.OK) && ch.Action != Skip {
			return true
		}
	}
	return false
}

func (p *Plan) tally() string {
	n := map[string]int{}
	for _, ch := range p.Changes {
		n[ch.Action]++
	}
	unchanged := n[string(kind.OK)] + n[Skip]
	if !p.Pending() {
		return sprintf("no changes: %d unchanged", unchanged)
	}
	var parts []string
	for _, c := range []struct {
		action, label string
	}{
		{string(kind.Create), "to create"},
		{string(kind.Update), "to update"},
		{RunNow, "to run"},
		{RunIf, "decided during apply"},
		{string(kind.Conflict), "in conflict (apply refuses)"},
	} {
		if n[c.action] > 0 {
			parts = append(parts, sprintf("%d %s", n[c.action], c.label))
		}
	}
	return strings.Join(append(parts, sprintf("%d unchanged", unchanged)), ", ")
}

// Render writes what apply did, one line per declaration.
func (r *Report) Render(w io.Writer) {
	wi, wr := 0, 0
	for _, o := range r.Outcomes {
		wi, wr = max(wi, len(o.ID)), max(wr, len(o.Result))
	}
	n := map[string]int{}
	var order []string
	for _, o := range r.Outcomes {
		line := fmt.Sprintf("  %-*s  %-*s  %s", wi, o.ID, wr, o.Result, o.Detail)
		fmt.Fprintln(w, strings.TrimRight(line, " "))
		if n[o.Result] == 0 {
			order = append(order, o.Result)
		}
		n[o.Result]++
	}
	var parts []string
	for _, res := range order {
		parts = append(parts, sprintf("%d %s", n[res], res))
	}
	fmt.Fprintf(w, "applied: %s\n", strings.Join(parts, ", "))
}
