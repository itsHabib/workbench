package gateauthorization

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestValidateExactAuthorizationClaimAndResult(t *testing.T) {
	artifact := validArtifact(t)
	if err := Validate(artifact); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data); err != nil {
		t.Fatal(err)
	}
	claim := validClaim(t, artifact)
	if err := ValidateClaim(claim, artifact); err != nil {
		t.Fatal(err)
	}
	result := ExecutionResult{
		SchemaVersion: SchemaVersion, ExecutionID: claim.ExecutionID,
		ClaimID: claim.ClaimID, Outcome: ExecutionMerged,
		MergeArgv:   append([]string(nil), claim.MergeArgv...),
		CompletedAt: claim.ClaimedAt.Add(time.Minute),
		MergeCommit: strings.Repeat("e", 40),
	}
	if err := ValidateResult(result, claim); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRefusesChangedAuthorization(t *testing.T) {
	tests := map[string]func(*Artifact){
		"unknown major": func(a *Artifact) { a.SchemaVersion = 2 },
		"wrong repo":    func(a *Artifact) { a.Request.Subject.Repo = "../other" },
		"short head":    func(a *Artifact) { a.Request.Subject.HeadSHA = "abc" },
		"base ref path": func(a *Artifact) { a.Request.Subject.BaseRef = "refs/heads/main" },
		"short base":    func(a *Artifact) { a.Request.Subject.BaseSHA = "abc" },
		"short mergebase": func(a *Artifact) {
			a.Request.Subject.MergeBaseSHA = "abc"
		},
		"bad action hash": func(a *Artifact) { a.Request.Gate.ActionHash = strings.Repeat("f", 64) },
		"unsupported":     func(a *Artifact) { a.Request.Outcome = "merged" },
		"bad evidence":    func(a *Artifact) { a.Request.EvidenceDigest = "sha256:nope" },
		"missing question": func(a *Artifact) {
			a.Request.JudgmentQuestion = ""
		},
		"expired bounds": func(a *Artifact) { a.Request.ExpiresAt = a.Request.IssuedAt },
		"long validity": func(a *Artifact) {
			a.Request.ExpiresAt = a.Request.IssuedAt.Add(MaxValidity + time.Second)
		},
		"wrong trust": func(a *Artifact) { a.Request.TrustRoot.Environment = "production" },
		"forged sha":  func(a *Artifact) { a.Request.MergeArgv[9] = strings.Repeat("f", 40) },
		"changed method": func(a *Artifact) {
			a.Request.MergeArgv[6] = "--merge"
		},
		"changed order": func(a *Artifact) {
			a.Request.MergeArgv[6], a.Request.MergeArgv[7] =
				a.Request.MergeArgv[7], a.Request.MergeArgv[6]
		},
		"admin": func(a *Artifact) { a.Request.MergeArgv = append(a.Request.MergeArgv, "--admin") },
		"wrong run": func(a *Artifact) {
			a.Receipt.WorkflowRunID++
		},
		"wrong actor": func(a *Artifact) {
			a.Receipt.ActorLogin = "ambient-user"
		},
		"wrong approval comment": func(a *Artifact) {
			a.Receipt.Comment = "approve"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			artifact := validArtifact(t)
			mutate(&artifact)
			if err := Validate(artifact); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}

func TestSemanticIdentitiesAreStable(t *testing.T) {
	artifact := validArtifact(t)
	first, err := AuthorizationID(artifact.Request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AuthorizationID(artifact.Request)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || ExecutionID(first) != ExecutionID(second) {
		t.Fatal("semantic identities changed across retry")
	}
}

func TestDecodeBoundsInput(t *testing.T) {
	if _, err := Decode(make([]byte, MaxArtifactBytes+1)); err == nil {
		t.Fatal("expected oversized artifact refusal")
	}
}

func TestDecodeToleratesUnknownOptionalFields(t *testing.T) {
	data, err := json.Marshal(validArtifact(t))
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	document["future_receipt"] = map[string]any{"value": true}
	data, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(data); err != nil {
		t.Fatal(err)
	}
}

func validArtifact(t *testing.T) Artifact {
	t.Helper()
	request := Request{
		Subject: Subject{
			Repo: "itsHabib/workbench", Number: 168,
			HeadSHA: strings.Repeat("b", 40), BaseRef: "main",
			BaseSHA: strings.Repeat("c", 40), MergeBaseSHA: strings.Repeat("d", 40),
		},
		Gate: Decision{
			RunID: "run_0123456789abcdef", ActionID: "act_0123456789abcdef",
			ActionHash: strings.Repeat("a", 64),
		},
		Outcome: OutcomeWouldMerge, EvidenceDigest: "sha256:" + strings.Repeat("9", 64),
		JudgmentQuestion: "May Gate execute this exact merge action?",
		IssuedAt:         time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		ReplayID:         "evt_" + strings.Repeat("8", 32),
		TrustRoot: TrustRoot{
			Kind: TrustGitHubEnvironment, Environment: DefaultEnvironment,
		},
	}
	request.ExpiresAt = request.IssuedAt.Add(20 * time.Minute)
	request.MergeArgv = ExpectedMergeArgv(request.Subject)
	id, err := AuthorizationID(request)
	if err != nil {
		t.Fatal(err)
	}
	receipt := ApprovalReceipt{
		WorkflowRunID: 1234, ActorLogin: "gate-approver", ActorID: 77,
		State: ApprovalStateApproved, SubmittedAt: request.IssuedAt.Add(time.Minute),
		Comment: ExpectedApprovalComment(id, request),
	}
	receipt.ReceiptDigest, err = ReceiptDigest(id, receipt)
	if err != nil {
		t.Fatal(err)
	}
	return Artifact{
		SchemaVersion: SchemaVersion, AuthorizationID: id,
		Request: request, Receipt: receipt,
	}
}

func validClaim(t *testing.T, artifact Artifact) ExecutionClaim {
	t.Helper()
	id, err := ClaimID(artifact)
	if err != nil {
		t.Fatal(err)
	}
	return ExecutionClaim{
		SchemaVersion: SchemaVersion, ClaimID: id,
		ExecutionID:      ExecutionID(artifact.AuthorizationID),
		AuthorizationID:  artifact.AuthorizationID,
		Authorization:    artifact,
		ExecutionGrantID: "grt_0123456789abcdef",
		Subject:          artifact.Request.Subject, Gate: artifact.Request.Gate,
		MergeArgv:             append([]string(nil), artifact.Request.MergeArgv...),
		ApprovalReceiptDigest: artifact.Receipt.ReceiptDigest,
		ClaimedAt:             artifact.Receipt.SubmittedAt.Add(time.Minute),
		ExpiresAt:             artifact.Request.ExpiresAt,
	}
}
