package main

import (
	"errors"
	"testing"
	"time"

	"github.com/itsHabib/workbench/contracts/reviewfindings"
	"github.com/itsHabib/workbench/contracts/reviewroute"
	"github.com/itsHabib/workbench/driverstate"
)

func TestValidateAddressDecisionJoinsAcceptedFindingSet(t *testing.T) {
	artifact := decisionArtifact()
	decision := addressDecision()
	if err := validateAddressDecision(decision, artifact); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAddressDecisionRejectsNonAddressAndStaleHead(t *testing.T) {
	artifact := decisionArtifact()
	decision := addressDecision()
	decision.Action = reviewroute.ActionStop
	assertAddressRefusal(t, validateAddressDecision(decision, artifact), "decision-not-address")

	decision = addressDecision()
	decision.Subject.HeadSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	assertAddressRefusal(t, validateAddressDecision(decision, artifact), "decision-subject-mismatch")
}

func TestValidateAddressDecisionRejectsFindingSetMismatch(t *testing.T) {
	artifact := decisionArtifact()
	decision := addressDecision()
	decision.Findings[0].ID = "other"
	assertAddressRefusal(t, validateAddressDecision(decision, artifact), "decision-findings-mismatch")

	decision = addressDecision()
	decision.Findings[0].Disposition = "deferred"
	decision.Findings[0].DeferReason = "later"
	assertAddressRefusal(t, validateAddressDecision(decision, artifact), "decision-empty")
}

func assertAddressRefusal(t *testing.T, err error, code string) {
	t.Helper()
	var refusal driverstate.AddressRefusal
	if !errors.As(err, &refusal) || refusal.Code != code {
		t.Fatalf("error = %v, want refusal %s", err, code)
	}
}

func addressDecision() reviewroute.Decision {
	return reviewroute.Decision{
		SchemaVersion: reviewroute.SchemaVersion,
		GeneratedAt:   time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
		Subject: reviewroute.Subject{
			Repo: "itsHabib/workbench", Number: 1,
			HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		PlanID: "rp_11111111111111111111111111111111", Cycle: 1,
		ContinuationWeight: 1, CumulativeWeight: 1,
		Action:        reviewroute.ActionAddress,
		ReasonCodes:   []string{"accepted_findings_require_address"},
		NextReviewers: []string{"codex"},
		Findings: []reviewroute.FindingState{{
			ID: "f1", Severity: "P1", Reviewers: []string{"codex"},
			Disposition: "fixed",
		}},
	}
}

func decisionArtifact() reviewfindings.Artifact {
	return reviewfindings.Artifact{
		SchemaVersion: reviewfindings.SchemaVersion,
		ArtifactID:    "rf_1",
		Decision:      reviewfindings.DecisionAddress,
		Subject: reviewfindings.Subject{
			Type: reviewfindings.SubjectPullRequest,
			Repo: "itsHabib/workbench", Number: 1,
			HeadSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		Producer: reviewfindings.Producer{
			ID: "test", Harness: "codex",
			GeneratedAt: time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC),
		},
		Panel: reviewfindings.Panel{
			Requested: []string{"codex"}, Completed: []string{"codex"},
			Missing: []string{},
		},
		Findings: []reviewfindings.Finding{{
			ID: "f1", Severity: "P1", Summary: "fix", Evidence: "evidence",
			Sources: []reviewfindings.Source{{
				Reviewer: "codex", CommentID: "1",
				URL: "https://example.test/comment/1",
			}},
		}},
	}
}
