package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "events.jsonl")
	index := filepath.Join(dir, "events.eq")
	if err := os.WriteFile(input, []byte("{\"ms\":5,\"kind\":\"test\"}\n{\"ms\":12,\"kind\":\"test\"}\n{\"ms\":20,\"kind\":\"build\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	buildArgs := []string{"build", "--out", index, "--column", "duration=ms:int", "--column", "kind=kind:string", input}
	if err := run(buildArgs, &out, &errOut); err != nil {
		t.Fatal(err, errOut.String())
	}
	if err := run(buildArgs, &out, &errOut); err == nil {
		t.Fatal("overwrote index")
	}
	out.Reset()
	if err := run([]string{"query", "--where", `duration >= 10 AND kind == "test"`, "--sum", "duration", "--json", index}, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Matches int
		Sum     int
		Rows    []map[string]any
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Matches != 1 || result.Sum != 12 || len(result.Rows) != 1 || result.Rows[0]["line"] != float64(2) {
		t.Fatal(out.String())
	}
	for _, args := range [][]string{{"query", "--where", "unknown == 3", index}, {"query", "--limit", "-1", index}, {"schema"}, {"unknown"}, {"build", "--out", index, input}, {"query", index, "--count"}} {
		if err := run(args, &out, &errOut); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	out.Reset()
	if err := run([]string{"schema", index}, &out, &errOut); err != nil || !strings.Contains(out.String(), "duration") {
		t.Fatal(out.String(), err)
	}
}

func TestOutputFailuresPropagate(t *testing.T) {
	if err := run([]string{"help"}, failingWriter{}, &bytes.Buffer{}); err == nil {
		t.Fatal("swallowed output error")
	}
}

type failingWriter struct{}

func (failingWriter) Write(_ []byte) (int, error) { return 0, os.ErrPermission }
