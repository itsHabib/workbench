package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/foundation"
)

// The baseline handles the common workload's idempotence, input change
// and retry cases correctly; the comparison with the typed API is about
// inspection and extension, not about the baseline being broken.
func TestBaselineHandlesTheCommonWorkload(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	open := func(f foundation.Fault) *foundation.Workspace {
		ws, err := foundation.Open(dir, f)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = ws.Close() })
		return ws
	}
	ws := open(foundation.Fault{})
	if err := run(ctx, ws, defaultText, io.Discard); err != nil {
		t.Fatal(err)
	}
	assertFile(t, dir, "upper.txt", "THE QUICK BROWN FOX\nJUMPS OVER THE LAZY DOG\n")
	assertFile(t, dir, "wordcount.txt", "9\n")

	// Unchanged re-run: no file is rewritten and no evidence is added.
	snap := snapshot(t, dir)
	if err := run(ctx, ws, defaultText, io.Discard); err != nil {
		t.Fatal(err)
	}
	snap.assertUnchanged(t, dir)

	// A failure in one step leaves the other step's completed work alone,
	// and the retry repeats only the failed step.
	next := "one two three\n"
	if err := run(ctx, open(foundation.Fault{Fail: "wordcount"}), next, io.Discard); err == nil {
		t.Fatal("run with injected failure succeeded")
	}
	assertFile(t, dir, "upper.txt", "ONE TWO THREE\n")
	upper := stat(t, dir, "upper.txt")
	if err := run(ctx, ws, next, io.Discard); err != nil {
		t.Fatal(err)
	}
	assertFile(t, dir, "wordcount.txt", "3\n")
	if !os.SameFile(upper, stat(t, dir, "upper.txt")) {
		t.Error("retry rewrote upper.txt, which had already completed")
	}
}

type snap struct {
	files    map[string]os.FileInfo
	evidence []byte
}

func snapshot(t *testing.T, dir string) snap {
	t.Helper()
	s := snap{files: map[string]os.FileInfo{}}
	for _, f := range []string{"input.txt", "upper.txt", "wordcount.txt"} {
		s.files[f] = stat(t, dir, f)
	}
	s.evidence, _ = os.ReadFile(filepath.Join(dir, ".wb/evidence.jsonl"))
	return s
}

func (s snap) assertUnchanged(t *testing.T, dir string) {
	t.Helper()
	for f, info := range s.files {
		if !os.SameFile(info, stat(t, dir, f)) {
			t.Errorf("%s was rewritten", f)
		}
	}
	if ev, _ := os.ReadFile(filepath.Join(dir, ".wb/evidence.jsonl")); string(ev) != string(s.evidence) {
		t.Error("evidence changed")
	}
}

func stat(t *testing.T, dir, f string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(filepath.Join(dir, f))
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func assertFile(t *testing.T, dir, f, want string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, f))
	if err != nil || string(b) != want {
		t.Errorf("%s = %q, %v; want %q", f, b, err, want)
	}
}
