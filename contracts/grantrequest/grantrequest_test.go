package grantrequest

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRequestSemanticIdentityAndRoundTrip(t *testing.T) {
	request := validRequest(t)
	if err := Validate(request); err != nil {
		t.Fatal(err)
	}
	id, err := AuthorizationID(request.Request)
	if err != nil {
		t.Fatal(err)
	}
	if id != request.AuthorizationID || !strings.HasPrefix(id, "gau_") {
		t.Fatalf("authorization id = %q, artifact = %q", id, request.AuthorizationID)
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var decoded RequestArtifact
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := Validate(decoded); err != nil {
		t.Fatal(err)
	}
}

func TestRequestRefusesWideningAndTampering(t *testing.T) {
	tests := map[string]func(*RequestArtifact){
		"version":  func(r *RequestArtifact) { r.SchemaVersion = "grant-request.v2" },
		"repo":     func(r *RequestArtifact) { r.Request.Subject.Repo = "../other" },
		"number":   func(r *RequestArtifact) { r.Request.Subject.Number = 0 },
		"head":     func(r *RequestArtifact) { r.Request.Subject.HeadSHA = "abc" },
		"action":   func(r *RequestArtifact) { r.Request.Action = "deploy" },
		"tier":     func(r *RequestArtifact) { r.Request.MaxTier = "T1" },
		"cycles":   func(r *RequestArtifact) { r.Request.MaxCycles = 4 },
		"validity": func(r *RequestArtifact) { r.Request.ExpiresAt = r.Request.ExpiresAt.Add(time.Second) },
		"identity": func(r *RequestArtifact) { r.AuthorizationID = "gau_" + strings.Repeat("f", 64) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			request := validRequest(t)
			mutate(&request)
			if err := Validate(request); err == nil {
				t.Fatal("expected refusal")
			}
		})
	}
}

func TestValidateRequestRefusesTraversalShapedRepo(t *testing.T) {
	request := validRequest(t).Request
	request.Subject.Repo = "../other"
	if err := ValidateRequest(request); err == nil {
		t.Fatal("traversal-shaped repo passed request validation")
	}
}

func TestDenialVocabulary(t *testing.T) {
	request := validRequest(t)
	denial := Denial{
		SchemaVersion: SchemaVersion, Request: request, Decision: DecisionDenied,
		Who: "@operator (U123)", At: request.Request.IssuedAt.Add(time.Minute),
		Reason: "denied in Slack",
	}
	if err := ValidateDenial(denial); err != nil {
		t.Fatal(err)
	}
	denial.Decision = "approved"
	if err := ValidateDenial(denial); err == nil {
		t.Fatal("expected unknown denial decision to fail")
	}
}

func TestSchemaPinsFixedScope(t *testing.T) {
	var schema struct {
		XVersion   string `json:"x-version"`
		Properties map[string]struct {
			Properties map[string]struct {
				Const any `json:"const"`
			} `json:"properties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(Schema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.XVersion != SchemaVersion {
		t.Fatalf("schema version = %q", schema.XVersion)
	}
	request := schema.Properties["request"].Properties
	if request["action"].Const != ActionMerge || request["max_tier"].Const != MaxTier {
		t.Fatalf("schema widened fixed scope: %v", request)
	}
	if got, ok := request["max_cycles"].Const.(float64); !ok || int(got) != MaxCycles {
		t.Fatalf("schema max_cycles = %#v", request["max_cycles"].Const)
	}
}

func TestDenialSchemaPinsTerminalVocabulary(t *testing.T) {
	var schema struct {
		XVersion   string `json:"x-version"`
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(DenialSchema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.XVersion != SchemaVersion {
		t.Fatalf("schema version = %q", schema.XVersion)
	}
	want := []string{DecisionDenied, DecisionExpired, DecisionStale}
	got := schema.Properties["decision"].Enum
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("denial decisions = %v, want %v", got, want)
	}
}

func validRequest(t *testing.T) RequestArtifact {
	t.Helper()
	request, err := New(Subject{
		Repo: "itsHabib/workbench", Number: 245,
		HeadSHA: strings.Repeat("a", 40),
	}, time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return request
}
