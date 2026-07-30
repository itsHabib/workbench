package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEvalInputs_readsFrozenFixtures(t *testing.T) {
	paths, err := resolvePaths(pathOptions{sourceDir: testSourceDir(t)})
	if err != nil {
		t.Fatal(err)
	}

	inputs, err := loadEvalInputs(paths.evalDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs.prompt) == 0 {
		t.Fatal("prompt is empty")
	}
	if len(inputs.schema) == 0 {
		t.Fatal("schema is empty")
	}
	if len(inputs.rows) != 51 {
		t.Fatalf("fixture rows = %d, want 51", len(inputs.rows))
	}
	if inputs.rows[0].Input == "" {
		t.Fatal("first fixture row has empty input")
	}
	if inputs.rows[0].Expected == "" {
		t.Fatal("first fixture row has empty expected")
	}
	if len(inputs.rows[0].Meta) == 0 {
		t.Fatal("first fixture row has empty meta")
	}
}

func TestLoadEvalInputs_fromNonRootWorkingDirectory(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(work); err != nil {
		t.Fatal(err)
	}

	paths, err := resolvePaths(pathOptions{sourceDir: testSourceDir(t)})
	if err != nil {
		t.Fatal(err)
	}

	inputs, err := loadEvalInputs(paths.evalDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs.rows) == 0 {
		t.Fatal("expected fixture rows")
	}
	if _, err := os.Stat(filepath.Join(paths.evalDir, "ci-lines-v2.jsonl")); err != nil {
		t.Fatalf("fixture path should remain absolute under repo root: %v", err)
	}
}
