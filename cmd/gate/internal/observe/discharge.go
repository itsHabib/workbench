package observe

import (
	"fmt"
	"io"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// ParkDischarge counts how every park the log has ever recorded was settled.
//
// It is a health metric, not an integrity one. The ratio that matters is
// supersession against judgment: a park discharged BY JUDGMENT is the loop
// working — a human or an auto-judge answered the question gate asked. A park
// discharged BY SUPERSESSION is a question gate asked and nobody ever answered,
// because a later run for the same PR overtook it. A rising supersession share
// means review churn upstream of the gate: PRs are being re-gated faster than
// their parks are being resolved, which is exactly the loop the review-cycle
// caps exist to stop.
//
// Like every observe surface it explains and never decides. It is computed from
// the same fold the inbox reduces against, so the two can never disagree about
// which terminal superseded which.
type ParkDischarge struct {
	Total int `json:"total"`
	// ByJudgment counts parks a judgment artifact was written against.
	ByJudgment int `json:"by_judgment"`
	// BySupersession counts parks no judgment ever answered, which a later
	// terminal for the same PR displaced.
	BySupersession int `json:"by_supersession"`
	// Moot counts parks still current for their subject whose pull request has
	// since finished — unanswered, and now unanswerable.
	Moot int `json:"moot"`
	// Live counts parks still genuinely awaiting the operator.
	Live int `json:"live"`
	// Unattributed counts parks whose subject cannot be resolved, so no
	// supersession or closure claim can be made about them either way.
	Unattributed int `json:"unattributed"`
}

// ParkDischargeReport classifies every escalation in the log.
func ParkDischargeReport(arts []state.Artifact) ParkDischarge {
	terms, facts := foldSubjectTerminals(arts)
	closed := buildClosureIndex(arts, facts)
	judged := judgedEscalations(arts)

	var rep ParkDischarge
	for _, a := range arts {
		if a.Kind != state.KindEscalation {
			continue
		}
		rep.Total++
		rep.classify(a, terms, closed, facts[a.Run], judged)
	}
	return rep
}

// classify files one escalation into exactly one class, judgment first: a park
// that WAS answered is answered regardless of what happened to the PR
// afterwards, and crediting the answer to supersession would understate the
// loop's health.
func (r *ParkDischarge) classify(a state.Artifact, terms subjectTerminals, closed closureIndex, f runFacts, judged map[string]bool) {
	if judged[a.ID] {
		r.ByJudgment++
		return
	}
	if f.Repo == "" || f.Number == 0 {
		r.Unattributed++
		return
	}
	key := subjectKey(f.Repo, f.Number)
	newest := terms.newest[key]
	if newest.artifact.ID != a.ID {
		r.BySupersession++
		return
	}
	if _, finished := closed.settles(f.Repo, f.Number, newest.order); finished {
		r.Moot++
		return
	}
	r.Live++
}

// judgedEscalations indexes every escalation a judgment was written against.
// The judgment is parented to the escalation it answers, so the parent link is
// the record of the answer.
func judgedEscalations(arts []state.Artifact) map[string]bool {
	judged := make(map[string]bool)
	for _, a := range arts {
		if a.Kind != state.KindJudgment {
			continue
		}
		for _, p := range a.Parents {
			judged[p] = true
		}
	}
	return judged
}

// RenderParkDischarge writes the metric as one audit line plus its breakdown. It
// prints nothing when the log holds no parks at all: a ratio over zero parks is
// noise, and audit's job is to be quiet until it has something to say.
func RenderParkDischarge(w io.Writer, r ParkDischarge) {
	if r.Total == 0 {
		return
	}
	fmt.Fprintf(w, "park discharge (%d): %d by judgment, %d by supersession, %d moot, %d awaiting\n",
		r.Total, r.ByJudgment, r.BySupersession, r.Moot, r.Live)
	if r.Unattributed > 0 {
		fmt.Fprintf(w, "  %d park(s) carry no resolvable PR subject and are classified none of the above\n", r.Unattributed)
	}
	if r.BySupersession == 0 {
		return
	}
	fmt.Fprintf(w, "  supersession share %.0f%% — parks a later run overtook before anyone answered them\n",
		100*float64(r.BySupersession)/float64(r.Total))
}
