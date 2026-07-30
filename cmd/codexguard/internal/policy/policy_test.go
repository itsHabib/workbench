package policy

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/itsHabib/workbench/contracts/automode"
)

const (
	testRepo = "itsHabib/workbench"
	testPR   = 172
	testHead = "608626c193e9c82fd8bf839a83a2ec3af7461000"
)

var testMerge = fmt.Sprintf(
	"gh pr merge %d -R %s --squash --delete-branch --match-head-commit %s",
	testPR, testRepo, testHead,
)

type fakeGate struct {
	rows  []ReadyMerge
	err   error
	calls int
	state string
}

func (f *fakeGate) Ready(_ context.Context, state string) ([]ReadyMerge, error) {
	f.calls++
	f.state = state
	return f.rows, f.err
}

type fakePRs struct {
	pr    PullRequest
	err   error
	calls int
	repo  string
	num   int
}

type contextGate struct{}

func (contextGate) Ready(ctx context.Context, _ string) ([]ReadyMerge, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func (f *fakePRs) Get(_ context.Context, repo string, number int) (PullRequest, error) {
	f.calls++
	f.repo = repo
	f.num = number
	return f.pr, f.err
}

func TestSafeReadsAndTestsPassAcrossEnvelopes(t *testing.T) {
	encoded := encodePowerShell("go test ./...")
	tests := []struct {
		name      string
		envelope  Envelope
		operation string
	}{
		{"direct read", Envelope{Kind: "shell", Shell: "direct", Command: "git status"}, "read"},
		{"branch read", Envelope{Kind: "shell", Shell: "direct", Command: "git branch --show-current"}, "read"},
		{"bash wrapper", Envelope{Kind: "shell", Shell: "bash", Command: `bash -lc "go test ./..."`}, "test"},
		{"sh script", Envelope{Kind: "shell", Shell: "sh", Command: "go vet ./..."}, "test"},
		{"powershell command", Envelope{Kind: "shell", Shell: "powershell", Command: `pwsh -Command "gh pr view 172 -R itsHabib/workbench"`}, "read"},
		{"powershell encoded", Envelope{Kind: "shell", Shell: "powershell", Command: "pwsh -EncodedCommand " + encoded}, "test"},
		{"cmd wrapper", Envelope{Kind: "shell", Shell: "cmd", Command: `cmd /c "golangci-lint run ./..."`}, "test"},
		{"local shell", Envelope{Kind: "local", Tool: "shell_command", Arguments: json.RawMessage(`{"command":"git diff"}`)}, "read"},
		{"local function shell", Envelope{Kind: "local", Tool: "functions.shell_command", Arguments: json.RawMessage(`{"command":"go test ./..."}`)}, "test"},
		{"mcp read", Envelope{Kind: "mcp", Tool: "mcp__workbench__driver_verify", Arguments: json.RawMessage(`{"run":"dsr_1"}`)}, "read"},
		{"code local", Envelope{Kind: "code", Code: `await tools.shell_command({"command":"go test ./..."})`}, "test"},
		{"code local function", Envelope{Kind: "code", Code: `await tools.functions.shell_command({"command":"git status"})`}, "read"},
		{"code mcp", Envelope{Kind: "code", Code: `return await tools.mcp__dossier__overview({"project":"workbench"});`}, "read"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gate := &fakeGate{}
			prs := &fakePRs{}
			got := evaluate(t, New(gate, prs), Request{Envelope: test.envelope})
			assertDecision(t, got, automode.OutcomePass, "safe.read_or_test")
			if got.Inputs.Action.Operation != test.operation {
				t.Fatalf("operation = %q, want %q", got.Inputs.Action.Operation, test.operation)
			}
			if gate.calls != 0 || prs.calls != 0 {
				t.Fatalf("safe action read authority evidence: gate=%d github=%d", gate.calls, prs.calls)
			}
		})
	}
}

func TestAuthorityMutationsNeverPass(t *testing.T) {
	tests := []struct {
		name    string
		command string
		rule    string
	}{
		{"gate mint", "gate grant -repo o/r -max-tier T3", "authority.mint"},
		{"custody mint", "custody grant -key tracker -actions write", "authority.mint"},
		{"custody keys", "custody keys set -name token", "authority.mint"},
		{"gate mutation", "gate judge -run run_1 -grant grt_1 -decision pass", "authority.state_mutation"},
		{"custody serve", "custody serve -addr 127.0.0.1:8127", "authority.state_mutation"},
		{"state deletion", "Remove-Item C:/pers/gate/state -Recurse", "authority.state_mutation"},
		{"force", "git push --force origin main", "authority.force_push"},
		{"force lease", "git push --force-with-lease origin main", "authority.force_push"},
		{"delete repo", "gh repo delete itsHabib/workbench --yes", "authority.repo_delete"},
		{"visibility", "gh repo edit itsHabib/workbench --visibility public", "authority.visibility_change"},
		{"admin merge", testMerge + " --admin", "authority.merge_admin"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := evaluate(t, New(&fakeGate{}, &fakePRs{}), shellRequest(test.command))
			assertDecision(t, got, automode.OutcomeBlock, test.rule)
			if got.Remedy == "" {
				t.Fatal("authority block has no remedy")
			}
		})
	}
}

func TestOpaqueAndUnknownInputsPark(t *testing.T) {
	tests := []Envelope{
		{Kind: "shell", Shell: "bash", Command: "git status && gh pr merge 172"},
		{Kind: "shell", Shell: "bash", Command: `bash -lc "go test ./... & gh pr merge 172 -R itsHabib/workbench --admin"`},
		{Kind: "shell", Shell: "cmd", Command: `cmd /c "go test ./... & gh pr merge 172 -R itsHabib/workbench --admin"`},
		{Kind: "shell", Shell: "cmd", Command: `cmd /c "git status %CODEXGUARD_DEMO%"`},
		{Kind: "shell", Shell: "cmd", Command: `cmd /c "git status !CODEXGUARD_DEMO!"`},
		{Kind: "shell", Shell: "cmd", Command: `cmd /c "go test -ex^ec gate ./..."`},
		{Kind: "shell", Shell: "powershell", Command: "go test ./... & gh pr merge 172 -R itsHabib/workbench --admin"},
		{Kind: "shell", Shell: "powershell", Command: "git status @(gh pr merge 172 -R itsHabib/workbench --admin)"},
		{Kind: "shell", Shell: "powershell", Command: "git status (gh pr merge 172 -R itsHabib/workbench --admin)"},
		{Kind: "shell", Shell: "powershell", Command: "go test @unsafeArgs"},
		{Kind: "shell", Shell: "bash", Command: "go test $UNSAFE_ARGS"},
		{Kind: "shell", Shell: "bash", Command: "echo $(gate grant -repo o/r)"},
		{Kind: "shell", Shell: "bash", Command: "git status > C:/pers/gate/state/log.jsonl"},
		{Kind: "shell", Shell: "cmd", Command: `cmd /c "git status > C:/pers/gate/state/log.jsonl"`},
		{Kind: "shell", Shell: "powershell", Command: "git status > C:/pers/gate/state/log.jsonl"},
		{Kind: "shell", Shell: "direct", Command: "git branch -D codex/work"},
		{Kind: "shell", Shell: "direct", Command: "rg --pre=gate evil ."},
		{Kind: "shell", Shell: "direct", Command: "rg --hostname-bin=gate evil ."},
		{Kind: "shell", Shell: "direct", Command: "rg --search-zip evil ."},
		{Kind: "shell", Shell: "direct", Command: "git diff --ext-diff"},
		{Kind: "shell", Shell: "direct", Command: "git show --textconv HEAD"},
		{Kind: "shell", Shell: "direct", Command: "git diff --output=C:/pers/gate/state/log.jsonl"},
		{Kind: "shell", Shell: "direct", Command: "go test -exec=gate ./..."},
		{Kind: "shell", Shell: "direct", Command: "go vet -vettool=gate ./..."},
		{Kind: "shell", Shell: "direct", Command: "go vet --toolexec=gate ./..."},
		{Kind: "shell", Shell: "direct", Command: "golangci-lint run --fix"},
		{Kind: "shell", Shell: "direct", Command: "golangci-lint run --trace-path C:/pers/gate/state/log.jsonl"},
		{Kind: "shell", Shell: "direct", Command: "golangci-lint run --output.json.path C:/pers/gate/state/log.jsonl"},
		{Kind: "shell", Shell: "direct", Command: "gate explain -run run_1 -html -out C:/pers/gate/state/log.jsonl"},
		{Kind: "shell", Shell: "direct", Command: "gate next -cpuprofile C:/pers/gate/state/log.jsonl"},
		{Kind: "shell", Shell: "direct", Command: "gh pr view 174 -R itsHabib/workbench --web"},
		{Kind: "shell", Shell: "powershell", Command: `Invoke-Expression "gh pr merge 172"`},
		{Kind: "shell", Shell: "powershell", Command: `iex "gate grant -repo o/r"`},
		{Kind: "shell", Shell: "powershell", Command: `Start-Process gh -ArgumentList "pr merge 172"`},
		{Kind: "mcp", Tool: "mcp__unknown__mutate", Arguments: json.RawMessage(`{}`)},
		{Kind: "local", Tool: "functions.read_file", Arguments: json.RawMessage(`{"path":"C:/pers/gate/keys"}`)},
		{Kind: "code", Code: `const x = 1; await tools.shell_command({"command":"git status"})`},
		{Kind: "future!"},
	}
	for i, envelope := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			got := evaluate(t, New(&fakeGate{}, &fakePRs{}), Request{Envelope: envelope})
			if got.Outcome != automode.OutcomePark {
				t.Fatalf("outcome = %q, want park", got.Outcome)
			}
			if got.Remedy == "" {
				t.Fatal("park has no remedy")
			}
		})
	}
}

func TestExactGateMergePassesOnlyForLiveExactHead(t *testing.T) {
	gate := &fakeGate{rows: []ReadyMerge{readyRow()}}
	prs := &fakePRs{pr: PullRequest{State: "OPEN", HeadSHA: testHead}}
	got := evaluate(t, New(gate, prs), shellRequestWithState(testMerge))

	assertDecision(t, got, automode.OutcomePass, "merge.exact_gate_command_live_head")
	if gate.calls != 1 || gate.state != "C:/gate/state" {
		t.Fatalf("Gate calls=%d state=%q", gate.calls, gate.state)
	}
	if prs.calls != 1 || prs.repo != testRepo || prs.num != testPR {
		t.Fatalf("GitHub call = %s#%d (%d calls)", prs.repo, prs.num, prs.calls)
	}
	if got.Inputs.Action.Operation != "github.pr.merge" {
		t.Fatalf("operation = %q", got.Inputs.Action.Operation)
	}
}

func TestEquivalentMergeEnvelopesUseSamePolicy(t *testing.T) {
	envelopes := []Envelope{
		{Kind: "shell", Shell: "direct", Command: testMerge},
		{Kind: "shell", Shell: "bash", Command: `bash -lc "` + testMerge + `"`},
		{Kind: "shell", Shell: "powershell", Command: `pwsh -Command "` + testMerge + `"`},
		{Kind: "shell", Shell: "powershell", Command: "& " + testMerge},
		{Kind: "shell", Shell: "cmd", Command: `cmd /c "` + testMerge + `"`},
		{Kind: "local", Tool: "shell_command", Arguments: mustJSON(map[string]string{"command": testMerge})},
		{Kind: "mcp", Tool: "mcp__local__shell_command", Arguments: mustJSON(map[string]string{"command": testMerge})},
		{Kind: "code", Code: `await tools.shell_command(` + string(mustJSON(map[string]string{"command": testMerge})) + `)`},
	}
	var routing automode.RoutingProjection
	for i, envelope := range envelopes {
		gate := &fakeGate{rows: []ReadyMerge{readyRow()}}
		prs := &fakePRs{pr: PullRequest{State: "OPEN", HeadSHA: testHead}}
		got := evaluate(t, New(gate, prs), Request{GateState: "C:/gate/state", Envelope: envelope})
		if i == 0 {
			routing = got.Routing()
		}
		if !reflect.DeepEqual(got.Routing(), routing) {
			t.Fatalf("envelope %d routing = %+v, want %+v", i, got.Routing(), routing)
		}
	}
}

func TestMergeRefusalMatrix(t *testing.T) {
	tests := []struct {
		name string
		req  Request
		gate *fakeGate
		prs  *fakePRs
		rule string
	}{
		{"missing state", shellRequest(testMerge), &fakeGate{rows: []ReadyMerge{readyRow()}}, livePRs(), "merge.gate_state_missing"},
		{"gate failure", shellRequestWithState(testMerge), &fakeGate{err: errors.New("boom")}, livePRs(), "merge.gate_read_failed"},
		{"no row", shellRequestWithState(testMerge), &fakeGate{}, livePRs(), "merge.ready_row_not_unique"},
		{"duplicate row", shellRequestWithState(testMerge), &fakeGate{rows: []ReadyMerge{readyRow(), readyRow()}}, livePRs(), "merge.ready_row_not_unique"},
		{"forged command", shellRequestWithState(testMerge + " "), &fakeGate{rows: []ReadyMerge{readyRow()}}, livePRs(), "merge.command_mismatch"},
		{"older row command", shellRequestWithState(testMerge), &fakeGate{rows: []ReadyMerge{{
			Run: "run_old", Repo: testRepo, Number: testPR, HeadSHA: testHead, MergeCommand: strings.Replace(testMerge, "--squash", "--merge", 1),
		}}}, livePRs(), "merge.command_mismatch"},
		{"gate head mismatch", shellRequestWithState(testMerge), &fakeGate{rows: []ReadyMerge{{
			Run: "run_new", Repo: testRepo, Number: testPR, HeadSHA: strings.Repeat("a", 40), MergeCommand: testMerge,
		}}}, livePRs(), "merge.gate_head_mismatch"},
		{"github ambiguity", shellRequestWithState(testMerge), &fakeGate{rows: []ReadyMerge{readyRow()}}, &fakePRs{err: errors.New("ambiguous")}, "merge.github_read_failed"},
		{"closed", shellRequestWithState(testMerge), &fakeGate{rows: []ReadyMerge{readyRow()}}, &fakePRs{pr: PullRequest{State: "CLOSED", HeadSHA: testHead}}, "merge.pr_not_open"},
		{"moved head", shellRequestWithState(testMerge), &fakeGate{rows: []ReadyMerge{readyRow()}}, &fakePRs{pr: PullRequest{State: "OPEN", HeadSHA: strings.Repeat("b", 40)}}, "merge.live_head_mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := evaluate(t, New(test.gate, test.prs), test.req)
			assertDecision(t, got, automode.OutcomeRefuse, test.rule)
			if got.Remedy == "" {
				t.Fatal("refusal has no remedy")
			}
		})
	}
}

func TestCanceledEvidenceReadRefuses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := New(contextGate{}, livePRs()).Evaluate(ctx, shellRequestWithState(testMerge))
	if err != nil {
		t.Fatal(err)
	}
	assertDecision(t, got, automode.OutcomeRefuse, "merge.gate_read_failed")
}

func TestOppositeMutationsOfAuthorizedCommandRefuse(t *testing.T) {
	authorized := evaluate(t, New(&fakeGate{rows: []ReadyMerge{readyRow()}}, livePRs()), shellRequestWithState(testMerge))
	mutations := []string{
		strings.Replace(testMerge, testHead, strings.Repeat("f", 40), 1),
		strings.Replace(testMerge, testRepo, "itsHabib/other", 1),
		strings.Replace(testMerge, fmt.Sprint(testPR), "173", 1),
		strings.Replace(testMerge, "--squash", "--merge", 1),
		testMerge + " ",
		" " + testMerge,
	}
	for i, command := range mutations {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			got := evaluate(t, New(&fakeGate{rows: []ReadyMerge{readyRow()}}, livePRs()), shellRequestWithState(command))
			if got.Outcome == automode.OutcomePass {
				t.Fatalf("opposite mutation passed: %q", command)
			}
			if got.ActionDigest == authorized.ActionDigest {
				t.Fatalf("opposite mutation kept authorized action identity: %q", command)
			}
		})
	}
}

func TestPropertyTruncatedSHANeverPasses(t *testing.T) {
	for n := 1; n < len(testHead); n++ {
		command := strings.Replace(testMerge, testHead, testHead[:n], 1)
		got := evaluate(t, New(&fakeGate{rows: []ReadyMerge{readyRow()}}, livePRs()), shellRequestWithState(command))
		if got.Outcome == automode.OutcomePass {
			t.Fatalf("%d-character SHA passed", n)
		}
	}
}

func TestActionDigestContainsNoRawCommandOrGateState(t *testing.T) {
	got := evaluate(t, New(&fakeGate{rows: []ReadyMerge{readyRow()}}, livePRs()), shellRequestWithState(testMerge))
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), testMerge) || strings.Contains(string(data), "C:/gate/state") {
		t.Fatalf("decision leaked raw command or state path: %s", data)
	}
}

func TestShellActionDigestBindsInnerCommandAndContext(t *testing.T) {
	base := evaluate(t, New(&fakeGate{}, &fakePRs{}), shellRequest("go test ./cmd/gate/..."))
	otherCommand := evaluate(t, New(&fakeGate{}, &fakePRs{}), shellRequest("go test ./cmd/custody/..."))
	if base.ActionDigest == otherCommand.ActionDigest {
		t.Fatal("different test commands share an action digest")
	}

	first := Request{Envelope: Envelope{
		Kind: "local", Tool: "functions.shell_command",
		Arguments: json.RawMessage(`{"command":"go test ./...","workdir":"C:/repo-one","login":false}`),
	}}
	second := first
	second.Envelope.Arguments = json.RawMessage(`{"command":"go test ./...","workdir":"C:/repo-two","login":true}`)
	firstDecision := evaluate(t, New(&fakeGate{}, &fakePRs{}), first)
	secondDecision := evaluate(t, New(&fakeGate{}, &fakePRs{}), second)
	if firstDecision.ActionDigest == secondDecision.ActionDigest {
		t.Fatal("different workdir/login contexts share an action digest")
	}
	data, err := json.Marshal(firstDecision)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "C:/repo-one") {
		t.Fatalf("decision leaked raw workdir: %s", data)
	}
}

func TestLocalShellElevationBlocksAndUnknownContextParks(t *testing.T) {
	elevated := Request{Envelope: Envelope{
		Kind: "local", Tool: "functions.shell_command",
		Arguments: json.RawMessage(`{"command":"git status","sandbox_permissions":"require_escalated","justification":"fixture"}`),
	}}
	got := evaluate(t, New(&fakeGate{}, &fakePRs{}), elevated)
	assertDecision(t, got, automode.OutcomeBlock, "authority.elevation")

	unknown := elevated
	unknown.Envelope.Arguments = json.RawMessage(`{"command":"git status","future_authority":true}`)
	got = evaluate(t, New(&fakeGate{}, &fakePRs{}), unknown)
	assertDecision(t, got, automode.OutcomePark, "envelope.unsupported")
}

func TestMalformedMergeAndHarnessDoNotLeakCanaries(t *testing.T) {
	request := shellRequestWithState(
		"gh pr merge 1 -R super-secret-token --match-head-commit another-secret",
	)
	got := evaluate(t, New(&fakeGate{}, &fakePRs{}), request)
	data, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, canary := range []string{"super-secret-token", "another-secret"} {
		if strings.Contains(string(data), canary) {
			t.Fatalf("decision leaked %q: %s", canary, data)
		}
	}
}

func evaluate(t *testing.T, evaluator Evaluator, request Request) automode.Decision {
	t.Helper()
	got, err := evaluator.Evaluate(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := automode.ValidateDecision(got); err != nil {
		t.Fatalf("invalid AutoDecisionV1: %v", err)
	}
	return got
}

func assertDecision(t *testing.T, got automode.Decision, outcome, rule string) {
	t.Helper()
	if got.Outcome != outcome || got.RuleFired != rule {
		t.Fatalf("decision = %s/%s, want %s/%s", got.Outcome, got.RuleFired, outcome, rule)
	}
}

func readyRow() ReadyMerge {
	return ReadyMerge{
		Run: "run_current", Repo: testRepo, Number: testPR, HeadSHA: testHead, MergeCommand: testMerge,
	}
}

func livePRs() *fakePRs {
	return &fakePRs{pr: PullRequest{State: "OPEN", HeadSHA: testHead}}
}

func shellRequest(command string) Request {
	return Request{Envelope: Envelope{Kind: "shell", Shell: "direct", Command: command}}
}

func shellRequestWithState(command string) Request {
	request := shellRequest(command)
	request.GateState = "C:/gate/state"
	return request
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func encodePowerShell(command string) string {
	units := utf16.Encode([]rune(command))
	data := make([]byte, len(units)*2)
	for i, unit := range units {
		binary.LittleEndian.PutUint16(data[i*2:], unit)
	}
	return base64.StdEncoding.EncodeToString(data)
}
