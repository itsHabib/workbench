package verify

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/contracts/reviewpanel"
)

// PanelCompleteness verifies that every repository-required reviewer completed
// a review of the exact head under judgment. It is deliberately separate from
// findings extraction: a clean result and an actionable result are different
// facts and compose monotonically in the reducer.
func PanelCompleteness(st *state.Store, run, evidenceID string, subject Subject) (state.Artifact, error) {
	a, err := st.Get(evidenceID)
	if err != nil {
		return state.Artifact{}, err
	}
	var panel reviewpanel.Evidence
	if err := json.Unmarshal(a.Body, &panel); err != nil {
		return state.Artifact{}, fmt.Errorf("verify: parse review panel evidence: %w", err)
	}
	if err := reviewpanel.Validate(panel); err != nil {
		return state.Artifact{}, fmt.Errorf("verify: invalid review panel evidence: %w", err)
	}
	v := Verdict{
		Subject: subject, Source: "review-panel-completeness",
		Producer: Producer{Class: ClassCode, Impl: "review-panel-v1"},
		Decision: DecisionPass, Tier: "T0", Confidence: 1,
	}
	if panel.Subject.Repo != subject.Repo || panel.Subject.Number != subject.Number ||
		panel.Subject.HeadSHA != subject.HeadSHA {
		v.Decision = DecisionEscalate
		v.Why = "review panel evidence does not match the exact judged head"
		return Record(st, run, []string{evidenceID}, v)
	}
	if len(panel.Declaration.Expected) == 0 || len(panel.Unknown) > 0 {
		v.Decision = DecisionEscalate
		v.Why = fmt.Sprintf("review panel state unknown: %s", strings.Join(panel.Unknown, ", "))
		return Record(st, run, []string{evidenceID}, v)
	}
	if len(panel.Pending) > 0 || len(panel.Missing) > 0 {
		v.Decision = DecisionEscalate
		v.Why = fmt.Sprintf(
			"review panel incomplete: completed=%d expected=%d pending=[%s] missing=[%s]",
			len(panel.Completed), len(panel.Declaration.Expected),
			strings.Join(panel.Pending, ", "), strings.Join(panel.Missing, ", "),
		)
		return Record(st, run, []string{evidenceID}, v)
	}
	v.Why = fmt.Sprintf(
		"all %d required reviewers completed the exact head declared by %s@%s",
		len(panel.Completed), panel.Declaration.Path, panel.Declaration.Revision,
	)
	return Record(st, run, []string{evidenceID}, v)
}
