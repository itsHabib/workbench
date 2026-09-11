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
