package hook

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/codexguard/internal/policy"
	"github.com/itsHabib/workbench/contracts/automode"
)

var fixtureTime = time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)

type memoryAudit struct {
	events []automode.AuditEvent
	err    error
}

func (a *memoryAudit) Append(event automode.AuditEvent) error {
	if a.err != nil {
		return a.err
	}
	if err := automode.ValidateAuditEvent(event); err != nil {
		return err
	}
	a.events = append(a.events, event)
	return nil
}

func (a *memoryAudit) Decision(invocationID string) (automode.Decision, bool, error) {
	if a.err != nil {
		return automode.Decision{}, false, a.err
	}
	var decision automode.Decision
	ok := false
	for _, event := range a.events {
		if event.Kind == automode.EventDecision && event.InvocationID == invocationID {
			decision = event.Decision
			ok = true
		}
	}
	return decision, ok, nil
}

type policyFunc func(context.Context, policy.Request) (automode.Decision, error)

func (fn policyFunc) Evaluate(ctx context.Context, request policy.Request) (automode.Decision, error) {
	return fn(ctx, request)
}

func TestGoldenPreToolUseFixtures(t *testing.T) {
	tests := []struct {
		name    string
		shell   string
		outcome string
		output  string
	}{
		{"pre-bash-block.json", "bash", automode.OutcomeBlock, "pre-bash-block.output.json"},
		{"pre-powershell-pass.json", "powershell", automode.OutcomePass, ""},
		{"pre-mcp-park.json", "bash", automode.OutcomePark, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			audit := &memoryAudit{}
			adapter := testAdapter(audit, test.shell)
			response, err := adapter.Handle(context.Background(), fixture(t, test.name))
			if err != nil {
				t.Fatal(err)
			}
			if len(audit.events) != 1 || audit.events[0].Kind != automode.EventDecision {
				t.Fatalf("events = %+v", audit.events)
			}
			if audit.events[0].Decision.Outcome != test.outcome {
				t.Fatalf("outcome = %q, want %q", audit.events[0].Decision.Outcome, test.outcome)
			}
			if test.outcome == automode.OutcomePass {
				if response != nil {
					t.Fatalf("pass response = %+v, want nil", response)
				}
				return
			}
			if response == nil || response.HookSpecificOutput.PermissionDecision != "deny" {
				t.Fatalf("non-pass response = %+v, want deny", response)
			}
			if test.output != "" {
				assertGoldenResponse(t, response, test.output)
			}
		})
	}
}

func TestGoldenPermissionRequestFixtures(t *testing.T) {
	tests := []struct {
		name     string
		shell    string
		outcome  string
		behavior string
		output   string
	}{
		{"permission-powershell-pass.json", "powershell", automode.OutcomePass, "", ""},
		{"permission-block.json", "bash", automode.OutcomeBlock, "deny", "permission-block.output.json"},
		{"permission-local-park.json", "bash", automode.OutcomePark, "", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			audit := &memoryAudit{}
			adapter := testAdapter(audit, test.shell)
			response, err := adapter.Handle(context.Background(), fixture(t, test.name))
			if err != nil {
				t.Fatal(err)
			}
			if len(audit.events) != 1 || audit.events[0].Decision.Outcome != test.outcome {
				t.Fatalf("events = %+v", audit.events)
			}
			if test.behavior == "" {
				if response != nil {
					t.Fatalf("%s response = %+v, want nil", test.outcome, response)
				}
				return
			}
			if response == nil || response.HookSpecificOutput.Decision == nil {
				t.Fatalf("response = %+v, want %s", response, test.behavior)
			}
			if response.HookSpecificOutput.Decision.Behavior != test.behavior {
				t.Fatalf("behavior = %q, want %q", response.HookSpecificOutput.Decision.Behavior, test.behavior)
			}
			assertGoldenResponse(t, response, test.output)
		})
	}
}

func TestPermissionRequestNeverWidensPolicy(t *testing.T) {
	for _, outcome := range []string{
		automode.OutcomePass,
		automode.OutcomePark,
		automode.OutcomeBlock,
		automode.OutcomeRefuse,
	} {
		t.Run(outcome, func(t *testing.T) {
			base := testDecision(t, "git status")
			base.Outcome = outcome
			base.Remedy = "operator review required"
			evaluator := policyFunc(func(context.Context, policy.Request) (automode.Decision, error) {
				return base, nil
			})
			audit := &memoryAudit{}
			adapter := New(evaluator, audit, "", "bash")
			adapter.now = func() time.Time { return fixtureTime }
			response, err := adapter.Handle(context.Background(), fixture(t, "permission-powershell-pass.json"))
			if err != nil {
				t.Fatal(err)
			}
			if response != nil && response.HookSpecificOutput.Decision != nil &&
				response.HookSpecificOutput.Decision.Behavior == "allow" {
				t.Fatalf("%s widened to allow", outcome)
			}
		})
	}
}

func TestPostToolUseSkipsNativeShellStatusUnknown(t *testing.T) {
	for _, name := range []string{"post-bash-success.json", "post-bash-failed.json"} {
		t.Run(name, func(t *testing.T) {
			audit := &memoryAudit{}
			adapter := testAdapter(audit, "powershell")
			if _, err := adapter.Handle(context.Background(), fixture(t, "pre-powershell-pass.json")); err != nil {
				t.Fatal(err)
			}
			response, err := adapter.Handle(context.Background(), fixture(t, name))
			if err != nil {
				t.Fatal(err)
			}
			if response != nil {
				t.Fatalf("PostToolUse response = %+v, want nil", response)
			}
			if len(audit.events) != 1 {
				t.Fatalf("status-unknown shell gained completion: %+v", audit.events)
			}
		})
	}
}

func TestPostToolUseAddsStructuredPassCompletion(t *testing.T) {
	decision := testDecision(t, "git status")
	evaluator := policyFunc(func(context.Context, policy.Request) (automode.Decision, error) {
		return decision, nil
	})
	audit := &memoryAudit{}
	adapter := New(evaluator, audit, "", "bash")
	adapter.now = func() time.Time { return fixtureTime }
	if _, err := adapter.Handle(context.Background(), fixture(t, "pre-mcp-pass.json")); err != nil {
		t.Fatal(err)
	}
	response, err := adapter.Handle(context.Background(), fixture(t, "post-mcp-success.json"))
	if err != nil {
		t.Fatal(err)
	}
	if response != nil {
		t.Fatalf("PostToolUse response = %+v, want nil", response)
	}
	if len(audit.events) != 2 {
		t.Fatalf("events = %d, want 2", len(audit.events))
	}
	completion := audit.events[1]
	if completion.Kind != automode.EventCompletion || completion.Completion.Status != automode.StatusSucceeded {
		t.Fatalf("completion = %+v, want succeeded", completion.Completion)
	}
	if completion.InvocationID != audit.events[0].InvocationID {
		t.Fatalf("completion invocation = %q, decision = %q", completion.InvocationID, audit.events[0].InvocationID)
	}
}

func TestPostToolUseDecisionLookupIsSessionBound(t *testing.T) {
	audit := &memoryAudit{}
	adapter := testAdapter(audit, "powershell")
	if _, err := adapter.Handle(context.Background(), fixture(t, "pre-powershell-pass.json")); err != nil {
		t.Fatal(err)
	}
	post := strings.Replace(string(fixture(t, "post-bash-success.json")), "thr_fixture", "thr_other", 1)
	if _, err := adapter.Handle(context.Background(), []byte(post)); err != nil {
		t.Fatal(err)
	}
	if len(audit.events) != 1 {
		t.Fatalf("cross-session post matched prior decision: %+v", audit.events)
	}
}

func TestPostToolUseCannotAuthorizeOrCompleteNonPass(t *testing.T) {
	audit := &memoryAudit{}
	adapter := testAdapter(audit, "bash")
	if _, err := adapter.Handle(context.Background(), fixture(t, "pre-bash-block.json")); err != nil {
		t.Fatal(err)
	}
	post := strings.Replace(
		string(fixture(t, "post-bash-success.json")),
		"call_git_status",
		"call_force_push",
		1,
	)
	if response, err := adapter.Handle(context.Background(), []byte(post)); err != nil || response != nil {
		t.Fatalf("response=%+v err=%v", response, err)
	}
	if len(audit.events) != 1 {
		t.Fatalf("non-pass gained completion: %+v", audit.events)
	}
}

func TestDecisionAppendFailureReturnsNoHookDecision(t *testing.T) {
	audit := &memoryAudit{err: errors.New("disk full")}
	adapter := testAdapter(audit, "bash")
	response, err := adapter.Handle(context.Background(), fixture(t, "pre-bash-block.json"))
	if err == nil || response != nil {
		t.Fatalf("response=%+v err=%v, want nil/error", response, err)
	}
}

func TestPolicyTimeoutReturnsNoHookDecision(t *testing.T) {
	evaluator := policyFunc(func(ctx context.Context, _ policy.Request) (automode.Decision, error) {
		<-ctx.Done()
		return automode.Decision{}, ctx.Err()
	})
	adapter := New(evaluator, &memoryAudit{}, "", "bash")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	response, err := adapter.Handle(ctx, fixture(t, "pre-bash-block.json"))
	if !errors.Is(err, context.Canceled) || response != nil {
		t.Fatalf("response=%+v err=%v, want nil/context canceled", response, err)
	}
}

func TestMalformedNativeEnvelopesRefuseAdapterOutput(t *testing.T) {
	input := fixture(t, "pre-bash-block.json")
	tests := [][]byte{
		[]byte(`{"hook_event_name":"PreToolUse"}`),
		append(input, []byte(` {}`)...),
		[]byte(strings.Replace(string(input), `"PreToolUse"`, `"FutureToolUse"`, 1)),
		[]byte(strings.Replace(string(input), `"tool_use_id": "call_force_push",`, "", 1)),
		[]byte(strings.Replace(string(input), `"permission_mode": "default"`, `"permission_mode": "future"`, 1)),
	}
	for i, data := range tests {
		response, err := testAdapter(&memoryAudit{}, "bash").Handle(context.Background(), data)
		if err == nil || response != nil {
			t.Fatalf("case %d: response=%+v err=%v", i, response, err)
		}
	}
}

func TestFileAuditConcurrentAppendAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "audit.jsonl")
	audit := NewFileAudit(path)
	decision := testDecision(t, "git status")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			event := automode.AuditEvent{
				SchemaVersion: automode.SchemaVersion,
				EventID:       "evt_" + time.Unix(0, int64(i+1)).Format("150405.000000000"),
				InvocationID:  "inv_" + time.Unix(0, int64(i+1)).Format("150405.000000000"),
				RecordedAt:    time.Unix(0, int64(i+1)).UTC(),
				Kind:          automode.EventDecision,
				Decision:      decision,
			}
			if err := audit.Append(event); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 16 {
		t.Fatalf("lines = %d, want 16", len(lines))
	}
	for _, line := range lines {
		if _, err := automode.DecodeAuditEvent([]byte(line)); err != nil {
			t.Fatalf("corrupt line: %v", err)
		}
	}
}

type failOnceFile struct {
	*os.File
	limit  int
	failed bool
}

func (f *failOnceFile) Write(data []byte) (int, error) {
	if f.failed {
		return f.File.Write(data)
	}
	f.failed = true
	n, err := f.File.Write(data[:min(f.limit, len(data))])
	if err != nil {
		return n, err
	}
	return n, errors.New("disk full")
}

func TestAuditAppendRollsBackPartialWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	prefix := []byte("{\"existing\":true}\n")
	if _, err := file.Write(prefix); err != nil {
		t.Fatal(err)
	}
	fault := &failOnceFile{File: file, limit: 7}
	if err := appendLine(fault, []byte("{\"partial\":true}\n")); err == nil {
		t.Fatal("append succeeded, want injected failure")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(data, prefix) {
		t.Fatalf("audit after rollback = %q, want %q", data, prefix)
	}
	if err := appendLine(file, []byte("{\"next\":true}\n")); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(prefix)+`{"next":true}`+"\n" {
		t.Fatalf("audit after retry = %q", data)
	}
}

func TestAuditContainsDigestsNotRawHookData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	adapter := New(
		policy.New(nil, nil),
		NewFileAudit(path),
		"",
		"powershell",
	)
	if _, err := adapter.Handle(context.Background(), fixture(t, "pre-powershell-pass.json")); err != nil {
		t.Fatal(err)
	}
	post := strings.Replace(
		string(fixture(t, "post-bash-success.json")),
		"working tree clean",
		"secret-response-canary",
		1,
	)
	if _, err := adapter.Handle(context.Background(), []byte(post)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"git status", `C:\workspace`, "secret-response-canary", "fixture-model"} {
		if strings.Contains(string(data), raw) {
			t.Fatalf("audit leaked %q: %s", raw, data)
		}
	}
}

func TestFailureMatrixPinsHonestHarnessSemantics(t *testing.T) {
	var rows []struct {
		Failure       string `json:"failure"`
		CodexBehavior string `json:"codex_behavior"`
		CoveredShell  string `json:"covered_shell"`
		Unsupported   string `json:"unsupported_local_or_mcp"`
	}
	if err := json.Unmarshal(fixture(t, "failure-matrix.json"), &rows); err != nil {
		t.Fatal(err)
	}
	want := []string{"missing_executable", "crash", "timeout", "malformed_stdout", "changed_untrusted_hash"}
	var got []string
	for _, row := range rows {
		got = append(got, row.Failure)
		if row.CoveredShell != "restrictive_rules_backstop" || row.Unsupported != "park" {
			t.Fatalf("dishonest row: %+v", row)
		}
		if row.CodexBehavior != "report_failure_and_continue" && row.CodexBehavior != "skip_until_trusted" {
			t.Fatalf("unexpected Codex behavior: %+v", row)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("failures = %v, want %v", got, want)
	}
}

func testAdapter(audit Audit, shell string) Adapter {
	adapter := New(policy.New(nil, nil), audit, "", shell)
	adapter.now = func() time.Time { return fixtureTime }
	return adapter
}

func testDecision(t *testing.T, command string) automode.Decision {
	t.Helper()
	decision, err := policy.New(nil, nil).Evaluate(context.Background(), policy.Request{
		Envelope: policy.Envelope{
			Kind:      "local",
			Tool:      "shell_command",
			Arguments: json.RawMessage(`{"command":` + mustJSON(command) + `}`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return decision
}

func mustJSON(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "hooks", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertGoldenResponse(t *testing.T, response *Response, name string) {
	t.Helper()
	var got, want any
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixture(t, name), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response = %s, want %s", data, fixture(t, name))
	}
}
