package tracelens

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parseShip(t *testing.T, lines ...string) Trajectory {
	t.Helper()
	tr, err := ParseShipEvents(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatalf("ParseShipEvents: %v", err)
	}
	return tr
}

func TestParseShipEvents_PairsToolCallLifecycle(t *testing.T) {
	tr := parseShip(t,
		`{"type":"tool_call","call_id":"c1","name":"read_file","status":"running"}`,
		`{"type":"tool_call","call_id":"c1","name":"read_file","status":"completed","args":{"path":"a.go"},"result":"package a"}`,
	)
	if len(tr.Steps) != 1 {
		t.Fatalf("lifecycle events must pair into one step, got %d", len(tr.Steps))
	}
	s := tr.Steps[0]
	if s.Tool != "read_file" {
		t.Fatalf("tool: want read_file, got %q", s.Tool)
	}
	if s.Args["path"] != "a.go" {
		t.Fatalf("args not merged from completed event: %+v", s.Args)
	}
	if s.Observation != "package a" {
		t.Fatalf("observation: got %q", s.Observation)
	}
	if s.OK == nil || !*s.OK {
		t.Fatalf("completed call must be OK=true, got %v", s.OK)
	}
}

func TestParseShipEvents_ParallelCallsKeepStartOrder(t *testing.T) {
	// Two calls run in parallel; completions arrive out of order. Steps sit at
	// the position each call first appeared.
	tr := parseShip(t,
		`{"type":"tool_call","call_id":"c1","name":"grep_search","status":"running"}`,
		`{"type":"tool_call","call_id":"c2","name":"read_file","status":"running"}`,
		`{"type":"tool_call","call_id":"c2","name":"read_file","status":"completed","result":"b"}`,
		`{"type":"tool_call","call_id":"c1","name":"grep_search","status":"completed","result":"a"}`,
	)
	if len(tr.Steps) != 2 {
		t.Fatalf("want 2 steps, got %d", len(tr.Steps))
	}
	if tr.Steps[0].Tool != "grep_search" || tr.Steps[1].Tool != "read_file" {
		t.Fatalf("start order not kept: %q, %q", tr.Steps[0].Tool, tr.Steps[1].Tool)
	}
	if tr.Steps[0].Index != 0 || tr.Steps[1].Index != 1 {
		t.Fatalf("indices must be sequential, got %d,%d", tr.Steps[0].Index, tr.Steps[1].Index)
	}
}

func TestParseShipEvents_CoalescesThinkingFragments(t *testing.T) {
	tr := parseShip(t,
		`{"type":"thinking","text":"Beginning design "}`,
		`{"type":"thinking","text":"of the feature."}`,
		`{"type":"tool_call","call_id":"c1","name":"read_file","status":"completed","result":"x"}`,
		`{"type":"thinking","text":"Now the store layer."}`,
	)
	if len(tr.Steps) != 3 {
		t.Fatalf("want thought, tool, thought — got %d steps", len(tr.Steps))
	}
	if tr.Steps[0].Thought != "Beginning design of the feature." {
		t.Fatalf("fragments must coalesce, got %q", tr.Steps[0].Thought)
	}
	if tr.Steps[2].Thought != "Now the store layer." {
		t.Fatalf("post-tool thought must be its own step, got %q", tr.Steps[2].Thought)
	}
}

func TestParseShipEvents_AssistantMessageFragments(t *testing.T) {
	tr := parseShip(t,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"I'll"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":" start now."}]}}`,
		`{"type":"thinking","text":"switching kinds splits"}`,
	)
	if len(tr.Steps) != 2 {
		t.Fatalf("want assistant + thinking steps, got %d", len(tr.Steps))
	}
	if tr.Steps[0].Thought != "I'll start now." {
		t.Fatalf("assistant fragments must coalesce, got %q", tr.Steps[0].Thought)
	}
}

func TestParseShipEvents_RunningOnlyCallHasUnknownOutcome(t *testing.T) {
	tr := parseShip(t,
		`{"type":"tool_call","call_id":"c1","name":"edit_file","status":"running","args":{"path":"x"}}`,
	)
	s := tr.Steps[0]
	if s.OK != nil {
		t.Fatalf("a call that never reported back must keep OK nil, got %v", *s.OK)
	}
	if s.Tool != "edit_file" || s.Args["path"] != "x" {
		t.Fatalf("running event must still carry tool+args: %+v", s)
	}
}

func TestParseShipEvents_FailedStatusMapsToError(t *testing.T) {
	tr := parseShip(t,
		`{"type":"tool_call","call_id":"c1","name":"run_terminal_cmd","status":"running"}`,
		`{"type":"tool_call","call_id":"c1","name":"run_terminal_cmd","status":"failed","result":"exit 1"}`,
	)
	s := tr.Steps[0]
	if !s.Failed() {
		t.Fatal("failed status must mark the step failed")
	}
	if s.Error != "exit 1" {
		t.Fatalf("failure text belongs in Error, got %q", s.Error)
	}
}

func TestParseShipEvents_SkipsStatusAndUnknownEvents(t *testing.T) {
	tr := parseShip(t,
		`{"type":"status","status":"CREATING"}`,
		`{"type":"rate_limit_event","detail":"x"}`,
		`{"type":"tool_call","call_id":"c1","name":"read_file","status":"completed","result":"y"}`,
	)
	if len(tr.Steps) != 1 {
		t.Fatalf("status/unknown events must not become steps, got %d", len(tr.Steps))
	}
}

func TestParseShipEvents_HeaderOnlyDialectsErrorClearly(t *testing.T) {
	cases := map[string]string{
		"claude": `{"type":"system","subtype":"init","session_id":"s"}`,
		"codex":  `{"type":"thread.started","thread_id":"t"}`,
	}
	for name, line := range cases {
		_, err := ParseShipEvents(strings.NewReader(line))
		if err == nil {
			t.Fatalf("%s header-only stream must have no analyzable steps", name)
		}
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("%s rejection must name the dialect, got %q", name, err)
		}
	}
}

func TestParseShipEvents_EmptyStreamErrors(t *testing.T) {
	if _, err := ParseShipEvents(strings.NewReader("")); err == nil {
		t.Fatal("an empty stream has nothing to analyze and must error")
	}
}

// TestParseShipEvents_LoopStillTripsAnalysis ties the adapter into the guard
// property: a ship-dialect stream with a mechanical loop must come out of
// Analyze pathological, so a stubbed detector core fails this test too.
func TestParseShipEvents_LoopStillTripsAnalysis(t *testing.T) {
	var lines []string
	for i := 0; i < 4; i++ {
		lines = append(lines,
			`{"type":"tool_call","call_id":"c`+string(rune('a'+i))+`","name":"run_tests","status":"completed","args":{"pkg":"./..."},"result":"FAIL TestX"}`,
		)
	}
	tr := parseShip(t, lines...)
	r := Analyze(tr, DefaultConfig())
	if r.Decision != DecisionBlock {
		t.Fatalf("a 4x identical-call ship trace must block, got %q", r.Decision)
	}
}

// TestParseShipEvents_RealDriverRunFixture proves the adapter on a real
// captured `ship driver` stream (agent-runner-seam-extract, failed
// timeout-near-cap at its 30m cap, then skipped by the driver): the stream
// parses, pairs its calls' args+results, and the analysis explains the cap
// burn — the agent looped re-editing one file.
func TestParseShipEvents_RealDriverRunFixture(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "testdata", "ship", "wf_01KVNKHBS61WJKZ9BVEQG6B5Y6", "events.ndjson"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	tr, err := ParseShipEvents(f)
	if err != nil {
		t.Fatalf("parse real run: %v", err)
	}
	// The captured stream holds 288 tool_call events — a few lifecycle events
	// per call (progressive running updates, then completed) pairing into 124
	// tool steps, 122 of them with both args and a result — plus coalesced
	// thought steps.
	if len(tr.Steps) < 100 {
		t.Fatalf("real run should pair into 100+ steps, got %d", len(tr.Steps))
	}
	paired := 0
	for _, s := range tr.Steps {
		if s.IsTool() && len(s.Args) > 0 && s.Observation != "" {
			paired++
		}
	}
	if paired < 100 {
		t.Fatalf("real run should pair args+results on 100+ calls, got %d", paired)
	}
	r := Analyze(tr, DefaultConfig())
	if r.Decision != DecisionBlock {
		t.Fatalf("the captured loop run must block, got %q", r.Decision)
	}
	if hasKind(r.Findings, "loop") == nil {
		t.Fatalf("the captured run's cap burn is a 6x edit loop; findings: %+v", r.Findings)
	}
}
