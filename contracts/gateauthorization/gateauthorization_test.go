package gateauthorization

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestValidateExactAuthorization(t *testing.T) {
	artifact := validArtifact()
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
}

func TestValidateRefusesChangedAuthorization(t *testing.T) {
	tests := map[string]func(*Artifact){
		"unknown major":   func(a *Artifact) { a.SchemaVersion = 2 },
		"wrong repo":      func(a *Artifact) { a.Subject.Repo = "../other" },
		"short head":      func(a *Artifact) { a.Subject.HeadSHA = "abc" },
		"bad action hash": func(a *Artifact) { a.Gate.ActionHash = strings.Repeat("f", 64) },
		"unsupported":     func(a *Artifact) { a.Outcome = "merged" },
		"expired bounds":  func(a *Artifact) { a.ExpiresAt = a.IssuedAt },
		"long validity":   func(a *Artifact) { a.ExpiresAt = a.IssuedAt.Add(MaxValidity + time.Second) },
		"wrong trust":     func(a *Artifact) { a.TrustRoot.Environment = "production" },
		"forged sha":      func(a *Artifact) { a.MergeArgv[9] = strings.Repeat("f", 40) },
		"changed method":  func(a *Artifact) { a.MergeArgv[6] = "--merge" },
		"changed order": func(a *Artifact) {
			a.MergeArgv[6], a.MergeArgv[7] = a.MergeArgv[7], a.MergeArgv[6]
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			artifact := validArtifact()
			mutate(&artifact)
			if err := Validate(artifact); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}

func TestDecodeBoundsInput(t *testing.T) {
	if _, err := Decode(make([]byte, MaxArtifactBytes+1)); err == nil {
		t.Fatal("expected oversized artifact refusal")
	}
}

func TestDecodeToleratesUnknownOptionalFields(t *testing.T) {
	data, err := json.Marshal(validArtifact())
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

func validArtifact() Artifact {
	hash := strings.Repeat("a", 64)
	subject := Subject{
		Repo: "itsHabib/workbench", Number: 168,
		HeadSHA: strings.Repeat("b", 40),
	}
	issued := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	return Artifact{
		SchemaVersion: SchemaVersion, AuthorizationID: "gau_" + hash,
		Subject: subject,
		Gate: Decision{
			RunID: "run_0123456789abcdef", ActionID: "act_0123456789abcdef",
			ActionHash: hash,
		},
		Outcome: OutcomeWouldMerge, MergeArgv: ExpectedMergeArgv(subject),
		IssuedAt: issued, ExpiresAt: issued.Add(30 * time.Minute),
		TrustRoot: TrustRoot{
			Kind: TrustGitHubEnvironment, Environment: DefaultEnvironment,
		},
	}
}
