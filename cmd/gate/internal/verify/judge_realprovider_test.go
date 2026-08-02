package verify

import (
	"os"
	"os/exec"
	"testing"
)

// End-to-end against a REAL provider CLI, driving gate's actual AutoJudge path
// and asserting the judgment decodes into a usable verdict. Opt-in: it needs a
// logged-in codex CLI and bills a real request, so CI never runs it.
//
//	GATE_E2E_REAL=1 go test -run TestRealProviderEndToEnd ./cmd/gate/internal/verify/
//
// The fakes cover shape; this covers reality. It exists because the provider
// contract broke in a way every fake missed — a live codex judgment carried an
// object-shaped producer and gate refused it as judgment_malformed at the exact
// moment a merge needed authorizing.
func TestRealProviderEndToEnd(t *testing.T) {
	if os.Getenv("GATE_E2E_REAL") != "1" {
		t.Skip("set GATE_E2E_REAL=1 to drive the real provider")
	}
	request := JudgmentRequestV1{
		Version:      JudgmentV1,
		Run:          "run_e2e_probe",
		EscalationID: "esc_e2e_probe",
		Subject:      Subject{Repo: "itsHabib/workbench", Number: 999, HeadSHA: "e2e0000000000000000000000000000000000000"},
		Grant:        JudgmentGrantV1{ID: "grt_e2e_probe", MaxTier: "T2"},
		Question:     "A test-only change fixes a temp-dir fixture. CI is green. Do the escalated concerns block the merge?",
		Context: judgePrompt + "\n\n" + artifactsBegin + "\n" +
			`{"kind":"verdict","body":{"source":"floor","producer":{"class":"code","impl":"triage-floor"},` +
			`"decision":"pass","tier":"T0","confidence":1,"why":"tests-only: test code"}}` + "\n" +
			artifactsEnd,
	}
	verdict, err := autoJudge(
		JudgeProviderCodex,
		request,
		t.TempDir(),
		sanitizedJudgeEnvironment(os.Environ()),
		resolveJudgeExecutable,
		exec.Command,
	)
	if err != nil {
		t.Fatalf("REAL provider round-trip failed: %v", err)
	}
	t.Logf("REAL provider OK — decision=%s tier=%s producer=%s why=%s",
		verdict.Decision, verdict.Tier, verdict.Producer.Impl, verdict.Why)
}
