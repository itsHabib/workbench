package tracelens

import (
	"strings"
	"testing"
)

func TestCodexServerScopesItemsAndKeepsUnknownOutcomes(t *testing.T) {
	input := `{"method":"item/started","params":{"threadId":"t","turnId":"a","item":{"id":"1","type":"commandExecution","command":"check","status":"inProgress"}}}
{"method":"item/completed","params":{"threadId":"t","turnId":"a","item":{"id":"1","type":"commandExecution","command":"check","aggregatedOutput":"pass","exitCode":0,"status":"completed"}}}
{"method":"item/completed","params":{"threadId":"t","turnId":"b","item":{"id":"1","type":"commandExecution","command":"other"}}}
`
	decoded, err := DecodeShipEvents(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	steps := decoded.Trajectory.Steps
	if decoded.Dialect != DialectCodexServer || len(steps) != 2 || steps[0].OK == nil || !*steps[0].OK || steps[1].OK != nil {
		t.Fatal(decoded)
	}
}

func TestCodexServerFailureAndMixedTrace(t *testing.T) {
	input := `{"method":"turn/completed","params":{"threadId":"t","turn":{"status":"failed","error":{"message":"provider disconnected"}}}}`
	decoded, err := DecodeShipEvents(strings.NewReader(input))
	if err != nil || decoded.Trajectory.DeclaredFailure != "provider disconnected" {
		t.Fatal(decoded, err)
	}
	if _, err := DecodeShipEvents(strings.NewReader(input + "\n" + `{"type":"result","subtype":"success"}`)); err == nil {
		t.Fatal("mixed producer trace accepted")
	}
}

func TestCodexServerFileChangeAndFinalAgentText(t *testing.T) {
	input := `{"method":"item/started","params":{"threadId":"t","turnId":"a","item":{"id":"edit","type":"fileChange","status":"inProgress","changes":[{"path":"main.go","kind":"update"}]}}}
{"method":"item/completed","params":{"threadId":"t","turnId":"a","item":{"id":"edit","type":"fileChange","status":"completed","changes":[{"path":"main.go","kind":"update"}]}}}
{"method":"item/started","params":{"threadId":"t","turnId":"a","item":{"id":"answer","type":"agentMessage","text":"partial"}}}
{"method":"item/completed","params":{"threadId":"t","turnId":"a","item":{"id":"answer","type":"agentMessage","text":"finished edit"}}}
`
	got, err := ParseCodexServerEvents(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Steps) != 2 {
		t.Fatal(got)
	}
	edit, answer := got.Steps[0], got.Steps[1]
	if edit.Tool != "file_change" || edit.OK == nil || !*edit.OK || !strings.Contains(edit.Observation, "main.go") || edit.Args["changes"] == nil {
		t.Fatal(edit)
	}
	if answer.Thought != "finished edit" || answer.Tool != "" || answer.OK != nil {
		t.Fatal(answer)
	}
}

func TestCodexServerRejectsUnsupportedActivityAlongsideSupportedSteps(t *testing.T) {
	step := `{"method":"item/completed","params":{"item":{"type":"commandExecution","id":"one","command":"check","exitCode":0}}}`
	for _, kind := range []string{"mcpToolCall", "dynamicToolCall", "futureTool"} {
		input := step + "\n" + `{"method":"item/completed","params":{"item":{"type":"` + kind + `","id":"two"}}}`
		if _, err := ParseCodexServerEvents(strings.NewReader(input)); err == nil || !strings.Contains(err.Error(), "unsupported app-server item") {
			t.Fatal(kind, err)
		}
	}
	input := step + "\n" + `{"method":"item/completed","params":{"item":{"type":"userMessage"}}}` + "\n" + `{"method":"item/completed","params":{"item":{"type":"reasoning"}}}`
	if got, err := ParseCodexServerEvents(strings.NewReader(input)); err != nil || len(got.Steps) != 1 {
		t.Fatal(got, err)
	}
}

func TestCodexServerTerminalOutcomesCannotBecomeCleanAfterSuccess(t *testing.T) {
	success := `{"method":"item/completed","params":{"threadId":"t","turnId":"later","item":{"id":"ok","type":"commandExecution","command":"check","exitCode":0,"status":"completed"}}}`
	cases := []struct{ name, event, decision, finding string }{
		{"declined command", `{"method":"item/completed","params":{"item":{"id":"no","type":"commandExecution","command":"refused","status":"declined"}}}`, "escalate", "tool_refusal"},
		{"declined patch", `{"method":"item/completed","params":{"item":{"id":"no","type":"fileChange","status":"declined"}}}`, "escalate", "tool_refusal"},
		{"declined beats contradictory exit", `{"method":"item/completed","params":{"item":{"id":"no","type":"commandExecution","command":"refused","status":"declined","exitCode":0}}}`, "escalate", "tool_refusal"},
		{"interrupted turn", `{"method":"turn/completed","params":{"turn":{"status":"interrupted"}}}`, "block", "run_failure"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr, err := ParseCodexServerEvents(strings.NewReader(tc.event + "\n" + success))
			if err != nil {
				t.Fatal(err)
			}
			report := Analyze(tr, DefaultConfig())
			if report.Decision != tc.decision || len(report.Findings) != 1 || report.Findings[0].Kind != tc.finding {
				t.Fatal(report)
			}
			if !tr.Steps[0].Failed() || !*tr.Steps[len(tr.Steps)-1].OK {
				t.Fatal("terminal failure or later success lost", tr)
			}
			if tc.finding == "tool_refusal" && tr.DeclaredFailure != "" {
				t.Fatal("tool refusal invented a whole-run failure", tr)
			}
		})
	}
}

func TestNeutralTracePreservesExplicitToolRefusal(t *testing.T) {
	tr, err := ParseJSONL(strings.NewReader(`{"tool":"check","ok":false,"declined":true,"error":"declined"}`))
	if err != nil {
		t.Fatal(err)
	}
	if report := Analyze(tr, DefaultConfig()); report.Decision != "escalate" || report.Findings[0].Kind != "tool_refusal" {
		t.Fatal(report)
	}
}
