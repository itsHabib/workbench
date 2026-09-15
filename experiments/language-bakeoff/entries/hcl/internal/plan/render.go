package plan

import (
	"fmt"
	"io"
	"strings"
)

var symbols = map[Action]string{Create: "+", Update: "~", Run: ">", Keep: "="}

// Render writes the human-readable plan.
func (p *Plan) Render(w io.Writer) {
	fmt.Fprintf(w, "Plan %s (source %s)\n\n", Short(p.Digest()), Short(p.SourceDigest))
	width := 0
	for _, s := range p.Steps {
		width = max(width, len(s.Address))
	}
	for _, s := range p.Steps {
		renderStep(w, s, width)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, p.Summary())
}

func renderStep(w io.Writer, s Step, width int) {
	lead := fmt.Sprintf("  %s %-*s  %-9s", symbols[s.Action], width, s.Address, s.Action)
	if len(s.Reasons) == 0 {
		fmt.Fprintln(w, strings.TrimRight(lead, " "))
	}
	for i, r := range s.Reasons {
		if i > 0 {
			lead = strings.Repeat(" ", len(lead))
		}
		fmt.Fprintf(w, "%s  %s\n", lead, r)
	}
	for _, d := range s.Detail {
		fmt.Fprintf(w, "      %s\n", d)
	}
}

// Summary is the one-line count of proposed changes.
func (p *Plan) Summary() string {
	n := map[Action]int{}
	for _, s := range p.Steps {
		n[s.Action]++
	}
	if p.Changes() == 0 {
		return fmt.Sprintf("No changes. %d unchanged.", n[Keep])
	}
	return fmt.Sprintf("Plan: %d to create, %d to update, %d to run, %d unchanged.", n[Create], n[Update], n[Run], n[Keep])
}
