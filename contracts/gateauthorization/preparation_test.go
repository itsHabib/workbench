package gateauthorization

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestPreparationRequestRoundTripAndStableIdentity(t *testing.T) {
	request := validPreparationRequest()
	id, err := PreparationID(request)
	if err != nil {
		t.Fatal(err)
	}
	artifact := PreparationRequestArtifact{
		SchemaVersion: SchemaVersion, PreparationID: id, Request: request,
	}
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePreparationRequest(data)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.PreparationID != id {
		t.Fatalf("preparation id = %s, want %s", decoded.PreparationID, id)
	}
	changed := request
	changed.Subject.HeadSHA = strings.Repeat("f", 40)
	changedID, err := PreparationID(changed)
	if err != nil {
		t.Fatal(err)
	}
	if changedID == id {
		t.Fatal("changed head retained preparation identity")
	}
	replayed := request
	replayed.ReplayID = "evt_" + strings.Repeat("b", 32)
	replayID, err := PreparationID(replayed)
	if err != nil {
		t.Fatal(err)
	}
	if replayID == id {
		t.Fatal("changed replay id retained preparation identity")
	}
}

func TestPreparationRequestRefusals(t *testing.T) {
	tests := map[string]func(*PreparationRequest){
		"stale head": func(r *PreparationRequest) { r.Subject.HeadSHA = "abc" },
		"bad grant":  func(r *PreparationRequest) { r.GrantID = "grt_bad" },
		"bad decision": func(r *PreparationRequest) {
			r.Decision = "merge"
		},
		"missing why": func(r *PreparationRequest) { r.Why = "" },
		"expired":     func(r *PreparationRequest) { r.ExpiresAt = r.IssuedAt },
		"long ttl": func(r *PreparationRequest) {
			r.ExpiresAt = r.IssuedAt.Add(MaxValidity + time.Second)
		},
		"bad replay": func(r *PreparationRequest) { r.ReplayID = "evt_bad" },
		"bad trust": func(r *PreparationRequest) {
			r.TrustRoot.Environment = "production"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := validPreparationRequest()
			mutate(&request)
			if err := ValidatePreparationRequest(request); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}

func TestPreparationApprovalRequiresExactComment(t *testing.T) {
	request := validPreparationRequest()
	id, err := PreparationID(request)
	if err != nil {
		t.Fatal(err)
	}
	receipt := ApprovalReceipt{
		WorkflowRunID: 7, ActorLogin: "independent-reviewer", ActorID: 8,
		State: ApprovalStateApproved, ObservedAt: request.IssuedAt.Add(time.Minute),
		Comment: ExpectedPreparationApprovalComment(id, request),
	}
	receipt.ReceiptDigest, err = ReceiptDigest(id, receipt)
	if err != nil {
		t.Fatal(err)
	}
	approval := PreparationApproval{
		SchemaVersion: SchemaVersion, PreparationID: id,
		Request: request, Receipt: receipt,
	}
	if err := ValidatePreparationApproval(approval); err != nil {
		t.Fatal(err)
	}
	approval.Receipt.Comment = "approve"
	if err := ValidatePreparationApproval(approval); err == nil {
		t.Fatal("mismatched comment accepted")
	}
}

func validPreparationRequest() PreparationRequest {
	now := time.Unix(1000, 0).UTC()
	return PreparationRequest{
		Subject: Subject{
			Repo: "o/r", Number: 17, HeadSHA: strings.Repeat("a", 40),
			BaseRef: "main", BaseSHA: strings.Repeat("b", 40),
			MergeBaseSHA: strings.Repeat("c", 40),
		},
		GrantID:  "grt_" + strings.Repeat("d", 16),
		Decision: "pass", Why: "exact head reviewed and green",
		IssuedAt: now, ExpiresAt: now.Add(10 * time.Minute),
		ReplayID: "evt_" + strings.Repeat("a", 32),
		TrustRoot: TrustRoot{
			Kind: TrustGitHubEnvironment, Environment: DefaultEnvironment,
		},
	}
}
