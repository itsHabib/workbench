package render

import (
	"fmt"
	"strings"

	"github.com/itsHabib/workbench/contracts/org"
)

// IntakeLane is one chartered lane's relationship to a work URI: whether the
// charter's scope covers it, and whether the lane already holds it. A lane
// appears in an intake report only when at least one of those is true — an
// out-of-scope hold is exactly the drift the report exists to surface.
type IntakeLane struct {
	Role  string    `json:"role"`
	Phase org.Phase `json:"phase"`
	// ScopeMatch is the charter entry that covers the work, empty when the
	// lane holds it out of scope.
	ScopeMatch string `json:"scope_match,omitempty"`
	Holds      bool   `json:"holds,omitempty"`
	// Err names a chain that could not be read; its lane cannot be judged.
	Err string `json:"error,omitempty"`
}

// Intake is the answer to "where does this work belong": the lanes that could
// hold the URI, the lanes that already do, and — when nothing covers it —
// that fact stated plainly instead of discovered an hour later.
type Intake struct {
	Work    string       `json:"work"`
	Tenant  string       `json:"tenant"`
	Covered bool         `json:"covered"`
	Lanes   []IntakeLane `json:"lanes,omitempty"`
}

// IntakeText renders the report for a human.
func IntakeText(in Intake) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "intake %s (tenant %s)\n", in.Work, in.Tenant)
	for _, l := range in.Lanes {
		fmt.Fprintf(&sb, "  %-28s %-10s %s\n", l.Role, l.Phase, laneNote(l))
	}
	if in.Covered {
		return sb.String()
	}
	fmt.Fprintf(&sb, "no chartered scope covers %s\n", in.Work)
	sb.WriteString("fix: charter a lane whose -scope covers it, or recharter an existing lane\n")
	return sb.String()
}

func laneNote(l IntakeLane) string {
	if l.Err != "" {
		return "unreadable: " + l.Err
	}
	if l.ScopeMatch != "" && l.Holds {
		return fmt.Sprintf("in scope (%s), already holds it", l.ScopeMatch)
	}
	if l.ScopeMatch != "" {
		return fmt.Sprintf("in scope (%s)", l.ScopeMatch)
	}
	return "holds it OUT OF SCOPE"
}
