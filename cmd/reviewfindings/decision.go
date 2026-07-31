package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/itsHabib/workbench/contracts/reviewfindings"
	"github.com/itsHabib/workbench/contracts/reviewroute"
	"github.com/itsHabib/workbench/driverstate"
)

func readAddressDecision(path string) (reviewroute.Decision, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return reviewroute.Decision{}, err
	}
	var decision reviewroute.Decision
	decoder := json.NewDecoder(bytes.NewReader(data))
	// ReviewDecisionV1 is a closed schema. Producers must bump schema_version
	// before adding fields so older consumers fail closed instead of silently
	// ignoring authorization data they do not understand.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decision); err != nil {
		return reviewroute.Decision{}, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return reviewroute.Decision{}, fmt.Errorf("review decision contains multiple JSON values")
	}
	if err := reviewroute.ValidateDecision(decision); err != nil {
		return reviewroute.Decision{}, err
	}
	return decision, nil
}

func validateAddressDecision(
	decision reviewroute.Decision,
	artifact reviewfindings.Artifact,
) error {
	if decision.Action != reviewroute.ActionAddress {
		return driverstate.AddressRefusal{
			Code:    "decision-not-address",
			Message: fmt.Sprintf("review decision action %s does not authorize address", decision.Action),
		}
	}
	switch {
	case !strings.EqualFold(decision.Subject.Repo, artifact.Subject.Repo):
		return subjectDecisionMismatch("repository")
	case decision.Subject.Number != artifact.Subject.Number:
		return subjectDecisionMismatch("pull request")
	case !strings.EqualFold(decision.Subject.HeadSHA, artifact.Subject.HeadSHA):
		return subjectDecisionMismatch("head")
	}
	accepted := make(map[string]struct{})
	for _, finding := range decision.Findings {
		// fixed + !changed means the fix was accepted but has not yet been
		// executed on this exact head; address is the execution boundary.
		if finding.Disposition == "fixed" && !finding.Changed {
			accepted[finding.ID] = struct{}{}
		}
	}
	if len(accepted) == 0 {
		return driverstate.AddressRefusal{
			Code: "decision-empty", Message: "review decision has no accepted findings",
		}
	}
	if len(accepted) != len(artifact.Findings) {
		return findingDecisionMismatch()
	}
	for _, finding := range artifact.Findings {
		if _, ok := accepted[finding.ID]; !ok {
			return findingDecisionMismatch()
		}
	}
	return nil
}

func subjectDecisionMismatch(field string) error {
	return driverstate.AddressRefusal{
		Code:    "decision-subject-mismatch",
		Message: "review decision " + field + " does not match findings artifact",
	}
}

func findingDecisionMismatch() error {
	return driverstate.AddressRefusal{
		Code:    "decision-findings-mismatch",
		Message: "review decision accepted finding ids do not match findings artifact",
	}
}
