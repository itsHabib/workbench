package verify

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

func TestProviderInvocationUsesOnlyBuiltInCLIs(t *testing.T) {
	cases := []struct {
		provider string
		name     string
		args     []string
	}{
		{
			provider: JudgeProviderClaude,
			name:     "claude",
			args: []string{
				"-p",
				"--safe-mode",
				"--tools", "",
			},
		},
		{
			provider: JudgeProviderCodex,
			name:     "codex",
			args: []string{
				"exec",
				"--ephemeral",
				"--sandbox", "read-only",
				"--skip-git-repo-check",
				"--ignore-user-config",
				"--ignore-rules",
				"--disable", "shell_tool",
				"--disable", "multi_agent",
				"-c", `forced_login_method="chatgpt"`,
				"-c", `service_tier="flex"`,
				"-c", `web_search="disabled"`,
				"-",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			got, err := providerInvocation(tc.provider)
			if err != nil {
				t.Fatal(err)
			}
			if got.name != tc.name || !slices.Equal(got.args, tc.args) {
				t.Fatalf("invocation = %s %v, want %s %v", got.name, got.args, tc.name, tc.args)
			}
		})
	}
}

func TestProviderInvocationRefusesCallerSelectedExecutable(t *testing.T) {
	for _, provider := range []string{
		`C:\tmp\judge.exe`,
		"/tmp/judge",
		"codex --dangerously-bypass-approvals-and-sandbox",
		"other",
		" codex ",
	} {
		if _, err := providerInvocation(provider); err == nil || !strings.Contains(err.Error(), "judge_provider_unsupported") {
			t.Fatalf("provider %q error = %v, want unsupported refusal", provider, err)
		}
	}
}

func TestAutoJudgeRunsBuiltInProviderAndRecordsIt(t *testing.T) {
	for _, provider := range []string{JudgeProviderClaude, JudgeProviderCodex} {
		t.Run(provider, func(t *testing.T) {
			request, _ := judgmentFixture()
			var gotName string
			var gotArgs []string
			factory := judgeHelperFactory(t, &gotName, &gotArgs)
			got, err := autoJudge(
				provider,
				request,
				t.TempDir(),
				judgeHelperEnvironment(""),
				fakeJudgeResolver,
				factory,
			)
			if err != nil {
				t.Fatal(err)
			}
			want, err := providerInvocation(provider)
			if err != nil {
				t.Fatal(err)
			}
			wantPath := filepath.Join(string(filepath.Separator), "trusted", want.name)
			if gotName != wantPath || !slices.Equal(gotArgs, want.args) {
				t.Fatalf("executed %s %v, want %s %v", gotName, gotArgs, wantPath, want.args)
			}
			wantProducer := provider + "-cli[" + want.name + "@sha256:" + strings.Repeat("a", 64) + "]:fixture-model"
			if got.Source != "auto-judgment" || got.Producer.Impl != wantProducer {
				t.Fatalf("provider provenance = source %q impl %q", got.Source, got.Producer.Impl)
			}
		})
	}
}

func TestAutoJudgeRefusesMalformedProviderOutput(t *testing.T) {
	request, _ := judgmentFixture()
	factory := judgeHelperFactory(t, nil, nil)
	_, err := autoJudge(
		JudgeProviderCodex,
		request,
		t.TempDir(),
		judgeHelperEnvironment("malformed"),
		fakeJudgeResolver,
		factory,
	)
	if err == nil || !strings.Contains(err.Error(), "judgment_malformed") {
		t.Fatalf("error = %v, want malformed-artifact refusal", err)
	}
}

func TestAutoJudgeReportsProviderFailure(t *testing.T) {
	request, _ := judgmentFixture()
	factory := judgeHelperFactory(t, nil, nil)
	_, err := autoJudge(
		JudgeProviderCodex,
		request,
		t.TempDir(),
		judgeHelperEnvironment("failed"),
		fakeJudgeResolver,
		factory,
	)
	if err == nil || !strings.Contains(err.Error(), "judge_provider_failed: helper failed") {
		t.Fatalf("error = %v, want provider failure", err)
	}
}

func TestResolveJudgeExecutableRecordsAbsolutePathAndDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "judge")
	if os.PathSeparator == '\\' {
		path += ".cmd"
	}
	contents := []byte("fixed provider wrapper")
	if err := os.WriteFile(path, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := resolveJudgeExecutable(path)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest := fmt.Sprintf("%x", sha256.Sum256(contents))
	if !filepath.IsAbs(got.path) || got.digest != wantDigest {
		t.Fatalf("resolved executable = %+v, want absolute path and digest %s", got, wantDigest)
	}
}

func TestSanitizedJudgeEnvironmentDropsAuthorityAndProviderSecrets(t *testing.T) {
	source := []string{
		"PATH=/bin",
		"HOME=/home/operator",
		"GATE_KEY=gate-secret",
		"GATE_STATE=/gate/state",
		"OPENAI_API_KEY=openai-secret",
		"ANTHROPIC_API_KEY=anthropic-secret",
		"GH_TOKEN=github-secret",
		"UNRELATED=value",
	}
	for _, provider := range []string{JudgeProviderClaude, JudgeProviderCodex} {
		t.Run(provider, func(t *testing.T) {
			got := sanitizedJudgeEnvironment(source)
			joined := strings.Join(got, "\n")
			if !strings.Contains(joined, "PATH=/bin") || !strings.Contains(joined, "HOME=/home/operator") {
				t.Fatalf("runtime paths were removed: %v", got)
			}
			for _, secret := range []string{"gate-secret", "openai-secret", "anthropic-secret", "github-secret", "UNRELATED"} {
				if strings.Contains(joined, secret) {
					t.Fatalf("secret or ambient variable %q survived: %v", secret, got)
				}
			}
			if strings.Contains(joined, "CLAUDE_CODE_SIMPLE") {
				t.Fatalf("simple mode would disable saved-login auth: %v", got)
			}
		})
	}
}

func fakeJudgeResolver(name string) (judgeExecutable, error) {
	return judgeExecutable{
		path:   filepath.Join(string(filepath.Separator), "trusted", name),
		digest: strings.Repeat("a", 64),
	}, nil
}

func judgeHelperEnvironment(mode string) []string {
	return append(
		sanitizedJudgeEnvironment(os.Environ()),
		"GATE_JUDGE_HELPER=1",
		"GATE_JUDGE_HELPER_MODE="+mode,
	)
}

func judgeHelperFactory(t *testing.T, gotName *string, gotArgs *[]string) judgeCommandFactory {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return func(name string, args ...string) *exec.Cmd {
		if gotName != nil {
			*gotName = name
		}
		if gotArgs != nil {
			*gotArgs = append((*gotArgs)[:0], args...)
		}
		return exec.Command(executable, "-test.run=^TestJudgeProviderHelper$")
	}
}

func TestJudgeProviderHelper(_ *testing.T) {
	if os.Getenv("GATE_JUDGE_HELPER") != "1" {
		return
	}
	switch os.Getenv("GATE_JUDGE_HELPER_MODE") {
	case "malformed":
		fmt.Fprint(os.Stdout, `{"version":`)
		os.Exit(0)
	case "failed":
		fmt.Fprint(os.Stderr, "helper failed")
		os.Exit(23)
	}
	var request JudgmentRequestV1
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(24)
	}
	artifact := JudgmentArtifactV1{
		Version:      JudgmentV1,
		Run:          request.Run,
		EscalationID: request.EscalationID,
		Subject:      request.Subject,
		Grant:        request.Grant,
		Question:     request.Question,
		Producer:     "fixture-model",
		Decision:     DecisionPass,
		Tier:         "T0",
		Confidence:   0.9,
		Why:          "the recorded exact-head evidence is safe",
	}
	if err := json.NewEncoder(os.Stdout).Encode(artifact); err != nil {
		fmt.Fprint(os.Stderr, err)
		os.Exit(25)
	}
	os.Exit(0)
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
