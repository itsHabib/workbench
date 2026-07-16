package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/cmd/tracelens/internal/tracelens"
)

func TestEvalMain_CommittedCorpusPasses(t *testing.T) {
	if code := evalMain([]string{filepath.Join("testdata", "corpus")}); code != 0 {
		t.Fatalf("evalMain exit = %d, want 0", code)
	}
}

func TestDecodeReportInput_RejectsDeclaredDialectMismatch(t *testing.T) {
	path := filepath.Join("testdata", "corpus", "ship-codex-healthy.ndjson")
	_, err := decodeReportInput(path, tracelens.DialectShipClaude)
	if err == nil {
		t.Fatal("declared/detected mismatch must fail")
	}
}

func TestReportMain_WritesMarkdownArtifact(t *testing.T) {
	out := filepath.Join(t.TempDir(), "report.md")
	input := filepath.Join("testdata", "corpus", "ship-claude-healthy.ndjson")
	code := reportMain([]string{"-dialect", string(tracelens.DialectShipClaude), "-output", out, input})
	if code != 0 {
		t.Fatalf("reportMain exit = %d", code)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("report artifact is empty")
	}
}
