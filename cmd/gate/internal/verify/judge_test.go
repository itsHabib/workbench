package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/quick"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

func judgmentFixture() (JudgmentRequestV1, JudgmentArtifactV1) {
	subject := Subject{Repo: "o/r", Number: 17, HeadSHA: "0123456789abcdef"}
	grant := JudgmentGrantV1{ID: "grt_123", MaxTier: "T2"}
	request := JudgmentRequestV1{
		Version:      JudgmentV1,
		Run:          "run_123",
		EscalationID: "esc_123",
		Subject:      subject,
		Grant:        grant,
		Question:     "do the findings block?",
		Context:      "recorded artifacts",
	}
	artifact := JudgmentArtifactV1{
		Version:      JudgmentV1,
		Run:          request.Run,
		EscalationID: request.EscalationID,
		Subject:      subject,
		Grant:        grant,
		Question:     request.Question,
		Producer:     "codex:gpt-5",
		Decision:     DecisionPass,
		Tier:         "T2",
		Confidence:   0.9,
		Why:          "the exact-head findings are resolved",
	}
	return request, artifact
}

func TestValidateJudgmentAcceptsExactBindings(t *testing.T) {
	request, artifact := judgmentFixture()
	got, err := ValidateJudgment(artifact, request)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != request.Subject || got.Producer.Impl != artifact.Producer || got.Decision != DecisionPass {
		t.Fatalf("validated verdict lost bindings: %+v", got)
	}
}

func TestValidateJudgmentRefusesAuthorityMismatches(t *testing.T) {
	request, base := judgmentFixture()
	cases := []struct {
		name string
		edit func(*JudgmentArtifactV1)
		code string
	}{
		{"wrong run", func(a *JudgmentArtifactV1) { a.Run = "run_other" }, "judgment_wrong_run"},
		{"wrong escalation", func(a *JudgmentArtifactV1) { a.EscalationID = "esc_other" }, "judgment_wrong_escalation"},
		{"stale head", func(a *JudgmentArtifactV1) { a.Subject.HeadSHA = "old" }, "judgment_stale_head"},
		{"wrong grant", func(a *JudgmentArtifactV1) { a.Grant.ID = "grt_other" }, "judgment_wrong_grant"},
		{"tier exceeds ceiling", func(a *JudgmentArtifactV1) { a.Tier = "T3" }, "judgment_tier_exceeded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			artifact := base
			tc.edit(&artifact)
			_, err := ValidateJudgment(artifact, request)
			if err == nil || !strings.Contains(err.Error(), tc.code) {
				t.Fatalf("error = %v, want code %s", err, tc.code)
			}
		})
	}
}

func TestJudgmentExactHeadProperty(t *testing.T) {
	property := func(current, submitted uint64) bool {
		if current == submitted {
			submitted++
		}
		request, artifact := judgmentFixture()
		request.Subject.HeadSHA = fmt.Sprintf("%016x", current)
		artifact.Subject = request.Subject
		artifact.Subject.HeadSHA = fmt.Sprintf("%016x", submitted)
		_, err := ValidateJudgment(artifact, request)
		return err != nil && strings.Contains(err.Error(), "judgment_stale_head")
	}
	cfg := &quick.Config{MaxCount: 100}
	if err := quick.Check(property, cfg); err != nil {
		t.Fatalf("exact-head property (seed is in counterexample): %v", err)
	}
}

func TestDecodeJudgmentArtifactRefusesMalformedAndUnknownFields(t *testing.T) {
	cases := []string{
		`{"version":`,
		`{"version":"gate-judgment-v1","future_authority":true}`,
		`{"version":"gate-judgment-v1"}`,
		`{"version":"gate-judgment-v1","confidence":null}`,
		`{} {}`,
	}
	for _, raw := range cases {
		if _, err := DecodeJudgmentArtifact(strings.NewReader(raw)); err == nil {
			t.Fatalf("malformed artifact accepted: %s", raw)
		}
	}
}

func TestDecodeJudgmentArtifactPreservesZeroConfidence(t *testing.T) {
	_, artifact := judgmentFixture()
	artifact.Confidence = 0
	raw, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeJudgmentArtifact(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got.Confidence != 0 {
		t.Fatalf("confidence = %v, want legitimate zero", got.Confidence)
	}
}

func TestValidateJudgmentTrimsProducerAndRefusesWhitespaceOnly(t *testing.T) {
	request, artifact := judgmentFixture()
	artifact.Producer = "  codex:gpt-5  "
	got, err := ValidateJudgment(artifact, request)
	if err != nil {
		t.Fatal(err)
	}
	if got.Producer.Impl != "codex:gpt-5" {
		t.Fatalf("producer = %q, want trimmed provenance", got.Producer.Impl)
	}
	artifact.Producer = " \t "
	if _, err := ValidateJudgment(artifact, request); err == nil || !strings.Contains(err.Error(), "judgment_missing_provenance") {
		t.Fatalf("whitespace-only producer error = %v", err)
	}
}

func TestAutoJudgeRequiresExplicitProvider(t *testing.T) {
	request, _ := judgmentFixture()
	_, err := AutoJudge("", request)
	if err == nil || !strings.Contains(err.Error(), "judge_provider_unconfigured") {
		t.Fatalf("error = %v, want actionable unconfigured-provider refusal", err)
	}
}

func TestNewJudgmentRequestUsesExactRecordedEscalation(t *testing.T) {
	subject := Subject{Repo: "o/r", Number: 17, HeadSHA: "head"}
	body, err := json.Marshal(map[string]any{"question": "is this safe?"})
	if err != nil {
		t.Fatal(err)
	}
	arts := []state.Artifact{{
		ID:   "esc_exact",
		Kind: state.KindEscalation,
		Body: body,
	}}
	request, err := NewJudgmentRequest(arts, "run_exact", "esc_exact", subject, "grt_exact", "T1")
	if err != nil {
		t.Fatal(err)
	}
	if request.Run != "run_exact" || request.Subject != subject || request.Question != "is this safe?" {
		t.Fatalf("request lost exact-state bindings: %+v", request)
	}
	if !bytes.Contains([]byte(request.Context), []byte("is this safe?")) {
		t.Fatal("provider context omitted the recorded question")
	}
}
