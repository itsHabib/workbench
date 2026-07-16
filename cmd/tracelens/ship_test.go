package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/tracelens/internal/tracelens"
)

func TestResolveRunRef_Directory(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveRunRef(dir)
	if err != nil {
		t.Fatalf("resolveRunRef: %v", err)
	}
	if got != filepath.Join(dir, "events.ndjson") {
		t.Fatalf("dir ref must resolve to its events file, got %q", got)
	}
}

func TestResolveRunRef_File(t *testing.T) {
	f := filepath.Join(t.TempDir(), "events.ndjson")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := resolveRunRef(f)
	if err != nil {
		t.Fatalf("resolveRunRef: %v", err)
	}
	if got != f {
		t.Fatalf("file ref must resolve to itself, got %q", got)
	}
}

func TestResolveRunRef_WorkflowIDUsesRunsDir(t *testing.T) {
	runs := t.TempDir()
	t.Setenv("SHIP_RUNS_DIR", runs)
	got, err := resolveRunRef("wf_01ABC")
	if err != nil {
		t.Fatalf("resolveRunRef: %v", err)
	}
	if got != filepath.Join(runs, "wf_01ABC", "events.ndjson") {
		t.Fatalf("wf id must resolve under SHIP_RUNS_DIR, got %q", got)
	}
}

func TestResolveRunRef_JunkErrors(t *testing.T) {
	if _, err := resolveRunRef("no-such-thing"); err == nil {
		t.Fatal("a non-path non-id ref must error")
	}
}

func TestConfigHome_HonorsAbsoluteXDG(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ship resolves APPDATA before XDG on windows")
	}
	t.Setenv("XDG_CONFIG_HOME", "/xdg/config")
	if got := configHome(); got != "/xdg/config" {
		t.Fatalf("absolute XDG_CONFIG_HOME must win, got %q", got)
	}
	t.Setenv("XDG_CONFIG_HOME", "relative/config")
	if got := configHome(); got == "relative/config" {
		t.Fatal("a relative XDG_CONFIG_HOME must fall through to ~/.config, matching ship")
	}
}

func TestGateCode(t *testing.T) {
	cases := map[string]int{
		tracelens.DecisionBlock:    1,
		tracelens.DecisionEscalate: 0,
		tracelens.DecisionPass:     0,
	}
	for decision, want := range cases {
		if got := gateCode(tracelens.Verdict{Decision: decision}); got != want {
			t.Fatalf("gateCode(%s): want %d, got %d", decision, want, got)
		}
	}
}

// TestGateCode_LoopTraceTripsGate is the end-to-end regression: a ship trace
// with a mechanical loop — the one detector that fires on cursor ship traces —
// must gate to exit 1 through the full analyze → verdict → gate path.
func TestGateCode_LoopTraceTripsGate(t *testing.T) {
	var lines []string
	for i := 0; i < 4; i++ {
		lines = append(lines,
			`{"type":"tool_call","call_id":"c`+string(rune('a'+i))+`","name":"run_tests","status":"completed","args":{"pkg":"./..."},"result":"FAIL TestX"}`,
		)
	}
	tr, err := tracelens.ParseShipEvents(strings.NewReader(strings.Join(lines, "\n")))
	if err != nil {
		t.Fatalf("ParseShipEvents: %v", err)
	}
	v := tracelens.Analyze(tr, tracelens.DefaultConfig()).Verdict()
	if v.Decision != tracelens.DecisionBlock {
		t.Fatalf("a looping ship trace must block, got %q", v.Decision)
	}
	if got := gateCode(v); got != 1 {
		t.Fatalf("a looping ship trace must gate to exit 1, got %d", got)
	}
}

func TestRunContextLine(t *testing.T) {
	dir := t.TempDir()
	if line := runContextLine(dir); line != "" {
		t.Fatalf("no result.json must yield no context line, got %q", line)
	}
	res := `{"status":"failed","durationMs":1800000,"failureCategory":"timeout-near-cap","failureDetail":"duration 30m (cap 30m)"}`
	if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte(res), 0o644); err != nil {
		t.Fatal(err)
	}
	line := runContextLine(dir)
	for _, want := range []string{"failed", "30m0s", "timeout-near-cap"} {
		if !strings.Contains(line, want) {
			t.Fatalf("context line missing %q: %q", want, line)
		}
	}
}
