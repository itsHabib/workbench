package tracelens

import (
	"strings"
	"testing"
)

func TestParseClaudeEvents_PairsParallelToolResults(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"inspect"},{"type":"tool_use","id":"a","name":"read","input":{"path":"a"}},{"type":"tool_use","id":"b","name":"read","input":{"path":"b"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"b","content":"B"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","content":"A","is_error":true}]}}`,
	}, "\n")
	tr, err := ParseClaudeEvents(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseClaudeEvents: %v", err)
	}
	if len(tr.Steps) != 3 {
		t.Fatalf("steps = %d, want 3", len(tr.Steps))
	}
	if tr.Steps[1].Tool != "read" || tr.Steps[1].Error != "A" || !tr.Steps[1].Failed() {
		t.Fatalf("first call = %+v", tr.Steps[1])
	}
	if tr.Steps[2].Observation != "B" || tr.Steps[2].Failed() {
		t.Fatalf("second call = %+v", tr.Steps[2])
	}
}

func TestParseClaudeEvents_IncompleteCallHasUnknownOutcome(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"read","input":{}}]}}`,
	}, "\n")
	tr, err := ParseClaudeEvents(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseClaudeEvents: %v", err)
	}
	if tr.Steps[0].OK != nil {
		t.Fatalf("incomplete call outcome = %v, want unknown", tr.Steps[0].OK)
	}
}

func TestParseClaudeEvents_MalformedLineHasContext(t *testing.T) {
	_, err := ParseClaudeEvents(strings.NewReader("{\n"))
	if err == nil || !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("malformed error = %v", err)
	}
}

func TestParseClaudeEvents_DuplicateToolUseIDReusesStep(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"read","input":{"path":"x"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"read","input":{"path":"x"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","content":"ok"}]}}`,
	}, "\n")
	tr, err := ParseClaudeEvents(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseClaudeEvents: %v", err)
	}
	if len(tr.Steps) != 1 {
		t.Fatalf("steps = %d, want 1 (duplicate id must reuse the step)", len(tr.Steps))
	}
	if tr.Steps[0].Observation != "ok" || tr.Steps[0].OK == nil {
		t.Fatalf("result must pair with the reused step: %+v", tr.Steps[0])
	}
}

func TestParseClaudeEvents_OrphanResultMaterializesFailureEvidence(t *testing.T) {
	in := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"gone","content":"exploded","is_error":true}]}}`
	tr, err := ParseClaudeEvents(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseClaudeEvents: %v", err)
	}
	if len(tr.Steps) != 1 {
		t.Fatalf("steps = %d, want 1 (orphan result must materialize)", len(tr.Steps))
	}
	if !tr.Steps[0].Failed() || tr.Steps[0].Error != "exploded" {
		t.Fatalf("orphan failure evidence lost: %+v", tr.Steps[0])
	}
}

func TestParseClaudeEvents_ErrorResultDeclaresRunFailure(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"read","input":{}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","content":"ok"}]}}`,
		`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"execution aborted"}`,
	}, "\n")
	tr, err := ParseClaudeEvents(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseClaudeEvents: %v", err)
	}
	if tr.DeclaredFailure != "execution aborted" {
		t.Fatalf("DeclaredFailure = %q, want the result error", tr.DeclaredFailure)
	}
	report := Analyze(tr, DefaultConfig())
	if report.Decision != DecisionBlock {
		t.Fatalf("decision = %q, want %q — an error result must not gate-pass on tool outcomes", report.Decision, DecisionBlock)
	}
}

func TestParseClaudeEvents_ResultOnlyFailureStillBlocks(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"result","subtype":"error_during_execution","is_error":true,"result":"aborted before any content"}`,
	}, "\n")
	tr, err := ParseClaudeEvents(strings.NewReader(in))
	if err != nil {
		t.Fatalf("a declared failure must decode, not error: %v", err)
	}
	report := Analyze(tr, DefaultConfig())
	if report.Decision != DecisionBlock {
		t.Fatalf("decision = %q, want %q for a result-only failure", report.Decision, DecisionBlock)
	}
}

func TestParseClaudeEvents_SuccessResultDeclaresNothing(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"all good"}`,
	}, "\n")
	tr, err := ParseClaudeEvents(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseClaudeEvents: %v", err)
	}
	if tr.DeclaredFailure != "" {
		t.Fatalf("DeclaredFailure = %q, want empty on success", tr.DeclaredFailure)
	}
}

func TestParseClaudeEvents_DuplicateResultLastWinsConsistently(t *testing.T) {
	in := strings.Join([]string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"a","name":"read","input":{}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","content":"FAIL","is_error":true}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"a","content":"recovered"}]}}`,
	}, "\n")
	tr, err := ParseClaudeEvents(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ParseClaudeEvents: %v", err)
	}
	s := tr.Steps[0]
	if s.Failed() || s.Error != "" || s.Observation != "recovered" {
		t.Fatalf("last result must replace contradictory fields: %+v", s)
	}
}
