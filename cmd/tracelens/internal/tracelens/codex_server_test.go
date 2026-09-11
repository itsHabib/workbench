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
