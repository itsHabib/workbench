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
	Phase org.Phase `json:"phase,omitempty"`
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
	Work   string `json:"work"`
	Tenant string `json:"tenant"`
	// Covered is true only when a READABLE lane's scope covers the work.
	// Indeterminate says the answer is not trustworthy: a chain could not be
	// read, so a lane that covers this work may exist and be invisible here.
	// A machine consumer that treats covered:false as "nothing covers it"
	// would otherwise charter a duplicate lane over an unreadable one.
	Covered       bool         `json:"covered"`
	Indeterminate bool         `json:"indeterminate,omitempty"`
	Unreadable    int          `json:"unreadable_lanes,omitempty"`
	Lanes         []IntakeLane `json:"lanes,omitempty"`
}

// Resolve stamps the derived verdict fields from the lanes collected so far.
func (in *Intake) Resolve() {
	in.Unreadable = unreadable(in.Lanes)
	in.Indeterminate = !in.Covered && in.Unreadable > 0
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
	if n := unreadable(in.Lanes); n > 0 {
		fmt.Fprintf(&sb, "no READABLE chartered scope covers %s — %d lane(s) unreadable and unjudged\n", in.Work, n)
		sb.WriteString("fix: repair or verify the unreadable chain(s) before chartering anything new\n")
		return sb.String()
	}
	fmt.Fprintf(&sb, "no chartered scope covers %s\n", in.Work)
	// Naming a verb that does not exist is worse here than anywhere else: this
	// line is read at the moment an agent is deciding how to route work, and it
	// is the only guidance it gets. `recharter` has no CLI writer — the verb was
	// withdrawn from #272 because nothing checks a widening — so terms are set
	// once and changing them is retire-then-charter, which is visible in the
	// chain instead of self-signed inside it (FOLLOWUPS).
	// And the fresh charter needs a NEW role id: retire is terminal for that
	// chain, so a charter under the retired id is refused `retired`.
	sb.WriteString("fix: charter a lane whose -scope covers it (terms are set once —\n")
	sb.WriteString("     changing an existing lane's scope means org retire, then a fresh charter\n")
	sb.WriteString("     under a NEW role id; a retired role's chain is terminal and cannot be re-chartered)\n")
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

// unreadable counts lanes whose chains could not be judged.
func unreadable(lanes []IntakeLane) int {
	n := 0
	for _, l := range lanes {
		if l.Err != "" {
			n++
		}
	}
	return n
}
