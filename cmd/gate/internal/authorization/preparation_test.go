package authorization

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/contracts/gateauthorization"
)

func TestAuthorizePreparationRequiresIndependentExactApproval(t *testing.T) {
	request := preparationRequest()
	id, err := gateauthorization.PreparationID(request)
	if err != nil {
		t.Fatal(err)
	}
	facts := ApprovalFacts{
		WorkflowRunID: 11, WorkflowActorID: 20, TriggeringActorID: 20,
		ObservedAt: request.IssuedAt.Add(time.Minute),
		Reviews: []ApprovalReview{{
			WorkflowRunID: 11, ActorLogin: "reviewer", ActorID: 21,
			State:        gateauthorization.ApprovalStateApproved,
			Comment:      gateauthorization.ExpectedPreparationApprovalComment(id, request),
			Environments: []string{gateauthorization.DefaultEnvironment},
		}},
	}
	approval, err := AuthorizePreparation(request, facts)
	if err != nil {
		t.Fatal(err)
	}
	if approval.PreparationID != id {
		t.Fatalf("preparation id = %s, want %s", approval.PreparationID, id)
	}

	dependent := facts
	dependent.Reviews = append([]ApprovalReview(nil), facts.Reviews...)
	dependent.Reviews[0].ActorID = facts.WorkflowActorID
	if _, err := AuthorizePreparation(request, dependent); !errors.Is(err, ErrApprovalDependent) {
		t.Fatalf("dependent approval error = %v", err)
	}

	mismatch := facts
	mismatch.Reviews = append([]ApprovalReview(nil), facts.Reviews...)
	mismatch.Reviews[0].Comment = "approve"
	if _, err := AuthorizePreparation(request, mismatch); !errors.Is(err, ErrApprovalMismatch) {
		t.Fatalf("mismatched approval error = %v", err)
	}

	duplicate := facts
	duplicate.Reviews = append(duplicate.Reviews, duplicate.Reviews[0])
	if _, err := AuthorizePreparation(request, duplicate); !errors.Is(err, ErrApprovalMismatch) {
		t.Fatalf("duplicate approval error = %v", err)
	}
}

func preparationRequest() gateauthorization.PreparationRequest {
	now := time.Unix(1000, 0).UTC()
	return gateauthorization.PreparationRequest{
		Subject: gateauthorization.Subject{
			Repo: "o/r", Number: 17, HeadSHA: strings.Repeat("a", 40),
			BaseRef: "main", BaseSHA: strings.Repeat("b", 40),
			MergeBaseSHA: strings.Repeat("c", 40),
		},
		GrantID:  "grt_" + strings.Repeat("d", 16),
		Decision: "pass", Why: "reviewed",
		IssuedAt: now, ExpiresAt: now.Add(10 * time.Minute),
		ReplayID: "evt_" + strings.Repeat("e", 32),
		TrustRoot: gateauthorization.TrustRoot{
			Kind:        gateauthorization.TrustGitHubEnvironment,
			Environment: gateauthorization.DefaultEnvironment,
		},
	}
}
