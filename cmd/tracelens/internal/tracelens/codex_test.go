package tracelens

import (
	"strings"
	"testing"
)

func TestParseCodexEvents_PairsInterleavedItems(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"thread.started","thread_id":"t"}`,
		`{"type":"item.started","item":{"id":"a","type":"command_execution","command":"go test","status":"in_progress"}}`,
		`{"type":"item.started","item":{"id":"b","type":"file_change","changes":[{"path":"x.go","kind":"update"}],"status":"in_progress"}}`,
		`{"type":"item.completed","item":{"id":"b","type":"file_change","changes":[{"path":"x.go","kind":"update"}],"status":"completed"}}`,
		`{"type":"item.completed","item":{"id":"a","type":"command_execution","command":"go test","aggregated_output":"FAIL","exit_code":1,"status":"failed"}}`,
		`{"type":"item.completed","item":{"id":"m","type":"agent_message","text":"finished"}}`,
	}, "\n")
	tr, err := ParseCodexEvents(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseCodexEvents: %v", err)
	}
	if len(tr.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(tr.Steps))
	}
	if tr.Steps[0].Tool != "command_execution" || !tr.Steps[0].Failed() || tr.Steps[0].Error != "FAIL" {
		t.Fatalf("command = %+v", tr.Steps[0])
	}
	if tr.Steps[1].Tool != "file_change" || tr.Steps[1].Failed() {
		t.Fatalf("file change = %+v", tr.Steps[1])
	}
	if tr.Steps[2].Thought != "finished" {
		t.Fatalf("message = %+v", tr.Steps[2])
	}
}

func TestParseCodexEvents_TurnFailedMaterializesFailure(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"thread.started","thread_id":"t"}`,
		`{"type":"item.completed","item":{"id":"a","type":"command_execution","command":"go test","aggregated_output":"ok","exit_code":0,"status":"completed"}}`,
		`{"type":"turn.failed","error":{"message":"provider crashed"}}`,
	}, "\n")
	tr, err := ParseCodexEvents(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseCodexEvents: %v", err)
	}
	if len(tr.Steps) != 2 {
		t.Fatalf("steps = %d, want 2 (turn failure must materialize)", len(tr.Steps))
	}
	last := tr.Steps[1]
	if !last.Failed() || last.Error != "provider crashed" {
		t.Fatalf("declared turn failure lost: %+v", last)
	}
	if tr.DeclaredFailure != "provider crashed" {
		t.Fatalf("DeclaredFailure = %q, want producer message", tr.DeclaredFailure)
	}
	report := Analyze(tr, DefaultConfig())
	if report.Decision != DecisionBlock {
		t.Fatalf("decision = %q, want %q — a producer-declared failure must not gate-pass", report.Decision, DecisionBlock)
	}
}

func TestParseCodexEvents_IncompleteItemHasUnknownOutcome(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"turn.started"}`,
		`{"type":"item.started","item":{"id":"a","type":"command_execution","command":"go test","status":"in_progress"}}`,
	}, "\n")
	tr, err := ParseCodexEvents(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseCodexEvents: %v", err)
	}
	if tr.Steps[0].OK != nil {
		t.Fatalf("incomplete item outcome = %v, want unknown", tr.Steps[0].OK)
	}
}

func TestParseCodexEvents_MalformedLineHasContext(t *testing.T) {
	_, err := ParseCodexEvents(strings.NewReader("{\n"))
	if err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("malformed error = %v", err)
	}
}
