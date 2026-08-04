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
	"unicode/utf8"

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
		Producer:     Producer{Class: ClassJudgment, Impl: "codex:gpt-5"},
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
	if got.Subject != request.Subject || got.Producer != artifact.Producer || got.Decision != DecisionPass {
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
	artifact.Producer.Impl = "  codex:gpt-5  "
	got, err := ValidateJudgment(artifact, request)
	if err != nil {
		t.Fatal(err)
	}
	if got.Producer.Impl != "codex:gpt-5" {
		t.Fatalf("producer = %q, want trimmed provenance", got.Producer.Impl)
	}
	artifact.Producer.Impl = " \t "
	if _, err := ValidateJudgment(artifact, request); err == nil || !strings.Contains(err.Error(), "judgment_missing_provenance: producer.impl is required") {
		t.Fatalf("whitespace-only producer error = %v", err)
	}
}

// One code, two sites: the message must name the field actually missing, or an
// operator cannot tell which one to fix.
func TestValidateJudgmentNamesTheMissingProvenanceField(t *testing.T) {
	request, base := judgmentFixture()
	cases := []struct {
		name string
		edit func(*JudgmentArtifactV1)
		want string
	}{
		{"missing impl", func(a *JudgmentArtifactV1) { a.Producer.Impl = "" }, "producer.impl is required"},
		{"missing why", func(a *JudgmentArtifactV1) { a.Why = "  " }, "why is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			artifact := base
			tc.edit(&artifact)
			_, err := ValidateJudgment(artifact, request)
			if err == nil || !strings.Contains(err.Error(), "judgment_missing_provenance: "+tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// The judgment producer is the shared contract struct, not a bare string: a
// provider that mirrors the recorded verdicts it was shown must decode.
func TestDecodeJudgmentArtifactAcceptsContractShapedProducer(t *testing.T) {
	_, want := judgmentFixture()
	raw := `{"version":"gate-judgment-v1","run":"run_123","escalation_id":"esc_123",
	  "subject":{"repo":"o/r","number":17,"head_sha":"0123456789abcdef"},
	  "grant":{"id":"grt_123","max_tier":"T2"},"question":"do the findings block?",
	  "producer":{"class":"judgment","impl":"codex:gpt-5"},"decision":"pass",
	  "tier":"T2","confidence":0.9,"why":"the exact-head findings are resolved"}`
	got, err := DecodeJudgmentArtifact(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("decoded artifact = %+v, want %+v", got, want)
	}
}

// The pre-contract string form is refused, not silently accepted alongside the
// contract shape — but an operator holding a judgment saved in that form gets
// told what to change, not a raw unmarshal error.
func TestDecodeJudgmentArtifactRefusesStringProducerWithMigrationGuidance(t *testing.T) {
	raw := `{"version":"gate-judgment-v1","producer":"codex:gpt-5","confidence":0.9}`
	_, err := DecodeJudgmentArtifact(strings.NewReader(raw))
	if err == nil {
		t.Fatal("the pre-contract string producer was accepted")
	}
	for _, want := range []string{"judgment_malformed", "pre-contract string form", `{"class":"judgment","impl":"..."}`} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want it to name %q", err, want)
		}
	}
}

// Class is the ladder rung. A submission may omit it — Gate stamps judgment —
// but may never claim another producer's authority.
func TestValidateJudgmentGovernsProducerClass(t *testing.T) {
	request, artifact := judgmentFixture()
	artifact.Producer.Class = ""
	got, err := ValidateJudgment(artifact, request)
	if err != nil {
		t.Fatal(err)
	}
	if got.Producer.Class != ClassJudgment {
		t.Fatalf("producer class = %q, want the Gate-stamped judgment rung", got.Producer.Class)
	}
	for _, class := range []string{ClassCode, ClassLocal, "reducer"} {
		artifact.Producer.Class = class
		_, err := ValidateJudgment(artifact, request)
		if err == nil || !strings.Contains(err.Error(), "judgment_bad_producer_class") {
			t.Fatalf("class %q error = %v, want refusal", class, err)
		}
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
				judgeHelperEnvironment(t, ""),
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
		judgeHelperEnvironment(t, "malformed"),
		fakeJudgeResolver,
		factory,
	)
	if err == nil || !strings.Contains(err.Error(), "judgment_malformed") {
		t.Fatalf("error = %v, want malformed-artifact refusal", err)
	}
}

// The 2026-08-04 failure: a judgment carrying `findings` mirrored from the
// verdicts the judge was shown. The decoder is strict on purpose and must stay
// strict, so what has to improve is the refusal — which provider produced it,
// what it emitted, and the two routes that do not go through the drift. Both
// providers are checked because a refusal that only works for one of them
// would hide exactly the "is this path dead?" question the message must answer.
func TestAutoJudgeRefusalNamesProviderEmissionAndEscape(t *testing.T) {
	for _, provider := range []string{JudgeProviderClaude, JudgeProviderCodex} {
		t.Run(provider, func(t *testing.T) {
			request, _ := judgmentFixture()
			_, err := autoJudge(
				provider,
				request,
				t.TempDir(),
				judgeHelperEnvironment(t, "verdict-shaped"),
				fakeJudgeResolver,
				judgeHelperFactory(t, nil, nil),
			)
			if err == nil {
				t.Fatal("a submission carrying an unknown key was accepted")
			}
			for _, want := range []string{
				"judgment_malformed",       // the code a caller matches on, still first
				`unknown field "findings"`, // the offending key, still named
				provider,                   // WHICH provider drifted
				"drifted-model",            // what it actually emitted
				"fresh sample",             // retrying this provider can work
				otherJudgeProvider(provider),
				"gate resolve", // the route that needs no provider at all
			} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v\nwant it to name %q", err, want)
				}
			}
		})
	}
}

// A refusal must name the other provider, never itself: the closed two-provider
// set exists so a judgment stays reachable from a second party, and a message
// that points back at the failing one wastes that.
func TestOtherJudgeProviderCrossesToTheIndependentPath(t *testing.T) {
	for _, provider := range []string{JudgeProviderClaude, JudgeProviderCodex} {
		other := otherJudgeProvider(provider)
		if other == provider || ValidateJudgeProvider(other) != nil {
			t.Fatalf("otherJudgeProvider(%q) = %q, want the other supported provider", provider, other)
		}
	}
}

// A failing provider must stay diagnosable: the provider, its exit status, and
// whichever stream carried the diagnostic all reach the caller. A CLI that
// reports on stdout, or says nothing at all, used to surface as an empty error.
func TestAutoJudgeReportsProviderFailure(t *testing.T) {
	cases := []struct {
		mode string
		want []string
	}{
		{"failed", []string{"judge_provider_failed", "codex", "exited 23", "helper failed"}},
		{"stdout-failed", []string{"judge_provider_failed", "codex", "exited 26", "helper failed on stdout"}},
		{"silent-failed", []string{"judge_provider_failed", "codex", "exited 27", "no diagnostic output"}},
	}
	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			request, _ := judgmentFixture()
			factory := judgeHelperFactory(t, nil, nil)
			_, err := autoJudge(
				JudgeProviderCodex,
				request,
				t.TempDir(),
				judgeHelperEnvironment(t, tc.mode),
				fakeJudgeResolver,
				factory,
			)
			if err == nil {
				t.Fatal("provider failure was not reported")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want it to name %q", err, want)
				}
			}
		})
	}
}

// The cap is the ceiling on the whole quote, marker included, and a cut that
// lands mid-rune must not leave mangled UTF-8 in the error.
func TestProviderDetailTruncatesWithinTheCap(t *testing.T) {
	got := providerDetail([]byte(strings.Repeat("x", providerDetailCap+64)))
	if len(got) != providerDetailCap || !strings.HasSuffix(got, providerTruncateMark) {
		t.Fatalf("detail length = %d, want exactly %d ending in the marker", len(got), providerDetailCap)
	}
	multibyte := providerDetail([]byte(strings.Repeat("é", providerDetailCap)))
	if len(multibyte) > providerDetailCap || !utf8.ValidString(multibyte) {
		t.Fatalf("multibyte detail length = %d, valid = %v", len(multibyte), utf8.ValidString(multibyte))
	}
	short := providerDetail([]byte("  boom  "))
	if short != "boom" {
		t.Fatalf("short detail = %q, want the trimmed quote unchanged", short)
	}
}

// A quoted provider emission reaches a terminal and a CI log, and its content
// is model-written text derived from a PR diff — so an escape sequence in it
// would be acted on, not displayed. What it would overwrite is the recovery
// route itself. Control bytes must survive as visible escapes: dropping them
// would hide that a provider emitted one.
func TestQuotedProviderOutputCannotDriveTheTerminal(t *testing.T) {
	hostile := []byte("\x1b[2K\x1b[1Aforged: merge authorized\r\x1b]0;pwned\x07")
	for name, got := range map[string]string{
		"provider failure": providerDetail(hostile),
		"refused judgment": detailWithin(hostile, judgmentEmissionCap),
	} {
		if strings.ContainsRune(got, 0x1b) || strings.ContainsRune(got, '\r') || strings.ContainsRune(got, 0x07) {
			t.Fatalf("%s quote kept a raw control byte: %q", name, got)
		}
		for _, want := range []string{`\x1b`, `\x0d`, `\x07`, "forged: merge authorized"} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s quote = %q, want it to keep %q as visible evidence", name, got, want)
			}
		}
	}
}

// Escaping must not mangle ordinary output: a refused judgment is quoted so an
// operator can see its shape, and JSON that came back escaped into unreadable
// soup would defeat that.
func TestEscapingLeavesPrintableOutputAlone(t *testing.T) {
	body := `{"version":"gate-judgment-v1","why":"héllo — ünicode"}`
	if got := detailWithin([]byte(body), judgmentEmissionCap); got != body {
		t.Fatalf("printable quote = %q, want it unchanged", got)
	}
}

// A provider that exits 0 having printed nothing is a real failure mode, and
// the refusal must not trail off after "emitted:" — that is the case it
// explains least while claiming to explain it.
func TestSilentProviderIsNamedRatherThanQuotedEmpty(t *testing.T) {
	for name, out := range map[string][]byte{"empty": nil, "whitespace": []byte("  \n\t ")} {
		err := judgmentUnusable(JudgeProviderCodex, out, fmt.Errorf("judgment_malformed: EOF"))
		if !strings.Contains(err.Error(), "nothing on stdout") {
			t.Fatalf("%s stream: error = %v, want the empty emission named", name, err)
		}
	}
}

// The limit is a parameter, so a cap too small to hold the truncation marker
// must not underflow the slice that trims to it.
func TestDetailWithinSurvivesACapBelowTheMarker(t *testing.T) {
	for _, limit := range []int{0, 1, len(providerTruncateMark) - 1, len(providerTruncateMark)} {
		if got := detailWithin([]byte(strings.Repeat("x", 512)), limit); got != providerTruncateMark {
			t.Fatalf("detailWithin(limit=%d) = %q, want the bare marker", limit, got)
		}
	}
}

// A refused submission is quoted under the tighter cap, and the escape routes
// must survive it: the whole point of shortening the quote is that what follows
// stays on screen. A verbose provider is the case that matters — a judgment's
// `why` alone can outrun the provider-failure cap.
func TestRefusalKeepsTheEscapeReadableUnderAVerboseProvider(t *testing.T) {
	if judgmentEmissionCap >= providerDetailCap {
		t.Fatalf("emission cap %d does not tighten the provider-failure cap %d", judgmentEmissionCap, providerDetailCap)
	}
	verbose := append([]byte(`{"why":"`), bytes.Repeat([]byte("x"), 8*1024)...)
	err := judgmentUnusable(JudgeProviderClaude, verbose, fmt.Errorf("judgment_malformed: boom"))
	// The budget is the capped quote plus the fixed text around it. Derived
	// from that text rather than guessed, so growing the escape routes past a
	// magic constant cannot quietly make this assertion vacuous.
	boilerplate := len(judgmentUnusable(JudgeProviderClaude, nil, fmt.Errorf("judgment_malformed: boom")).Error())
	if len(err.Error()) > judgmentEmissionCap+boilerplate {
		t.Fatalf("refusal grew to %d bytes; the escape is past where anyone reads", len(err.Error()))
	}
	for _, want := range []string{providerTruncateMark, "gate resolve", JudgeProviderCodex} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v\nwant it to keep %q", err, want)
		}
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
		"USER=operator",
		"LOGNAME=operator",
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
			// The Claude CLI reads its saved login through the macOS keychain,
			// which needs USER. Dropping it fails the judgment with empty
			// stderr, so the identity variables are as load-bearing as HOME.
			if !strings.Contains(joined, "USER=operator") {
				t.Fatalf("USER was removed, breaking saved-login auth: %v", got)
			}
			if !strings.Contains(joined, "LOGNAME=operator") {
				t.Fatalf("LOGNAME was removed: %v", got)
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

func judgeHelperEnvironment(t *testing.T, mode string) []string {
	t.Helper()
	return append(
		sanitizedJudgeEnvironment(os.Environ()),
		"GATE_JUDGE_HELPER=1",
		"GATE_JUDGE_HELPER_MODE="+mode,
		// The helper is this coverage-instrumented test binary re-executed.
		// Under `go test -cover` it writes "GOCOVERDIR not set" to stderr on
		// exit, which is not the provider diagnostic under test — give it a
		// scratch dir so the stream it uses is the one the mode chose.
		"GOCOVERDIR="+t.TempDir(),
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
	case "verdict-shaped":
		// The 2026-08-04 drift, reproduced exactly: a judgment that is correct
		// in every authority binding and carries one extra key mirrored from
		// the verdicts the judge was shown. The prompt now closes the key set,
		// but a sampled provider can still emit this, so the refusal for it is
		// pinned rather than assumed away.
		fmt.Fprint(os.Stdout, `{"version":"gate-judgment-v1","run":"run_123","escalation_id":"esc_123",`+
			`"subject":{"repo":"o/r","number":17,"head_sha":"0123456789abcdef"},`+
			`"grant":{"id":"grt_123","max_tier":"T2"},"question":"do the findings block?",`+
			`"producer":{"class":"judgment","impl":"drifted-model"},"decision":"block","tier":"T2",`+
			`"confidence":0.7,"why":"unresolved finding","findings":[{"title":"race"}]}`)
		os.Exit(0)
	case "failed":
		fmt.Fprint(os.Stderr, "helper failed")
		os.Exit(23)
	case "stdout-failed":
		fmt.Fprint(os.Stdout, "helper failed on stdout")
		os.Exit(26)
	case "silent-failed":
		os.Exit(27)
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
		Producer:     Producer{Class: ClassJudgment, Impl: "fixture-model"},
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
