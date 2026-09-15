package baseline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// run executes pipeline.sh against ws with NOTES_TEXT and any extra
// environment, returning combined output and whether it succeeded.
func run(t *testing.T, ws, text string, env ...string) (string, bool) {
	t.Helper()
	script, err := filepath.Abs("pipeline.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", script, ws)
	cmd.Env = append(append(os.Environ(), "NOTES_TEXT="+text), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "<absent>"
	}
	return string(b)
}

func mtime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.ModTime()
}

func want(t *testing.T, got string, lines ...string) {
	t.Helper()
	for _, l := range lines {
		if !strings.Contains(got, l) {
			t.Errorf("output lacks %q; got:\n%s", l, got)
		}
	}
}

// The baseline meets the bar on cases 1, 2, 3 and 5: it converges, repeats
// nothing when current, reruns downstream steps on input change, and a
// retry after a partial failure skips the completed step.
func TestBaselineCommonCases(t *testing.T) {
	ws := t.TempDir()
	out, ok := run(t, ws, "the quick brown fox")
	if !ok {
		t.Fatal(out)
	}
	want(t, out, "notes: wrote notes.txt", "shout: ran", "count: ran")
	if got := read(t, filepath.Join(ws, "notes.upper.txt")); got != "THE QUICK BROWN FOX\n" {
		t.Errorf("upper = %q", got)
	}

	upper := mtime(t, filepath.Join(ws, "notes.upper.txt"))
	out, _ = run(t, ws, "the quick brown fox")
	want(t, out, "notes: unchanged", "shout: current", "count: current")
	if !mtime(t, filepath.Join(ws, "notes.upper.txt")).Equal(upper) {
		t.Error("an unchanged rerun rewrote notes.upper.txt")
	}

	out, _ = run(t, ws, "the quick red fox")
	want(t, out, "notes: wrote notes.txt", "shout: ran", "count: ran")

	shim := t.TempDir()
	if err := os.WriteFile(filepath.Join(shim, "awk"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, ok = run(t, ws, "the quick grey fox", "PATH="+shim+":"+os.Getenv("PATH"))
	if ok {
		t.Fatal("the injected failure should stop the script")
	}
	want(t, out, "notes: wrote notes.txt", "shout: ran")
	upper = mtime(t, filepath.Join(ws, "notes.upper.txt"))
	out, ok = run(t, ws, "the quick grey fox")
	if !ok {
		t.Fatal(out)
	}
	want(t, out, "notes: unchanged", "shout: current", "count: ran")
	if !mtime(t, filepath.Join(ws, "notes.upper.txt")).Equal(upper) {
		t.Error("the retry repeated the completed shout step")
	}
}

// Case 4 has no counterpart: the script has no separate plan to go stale.
// It acts on the workspace as it finds it, so a hand edit is overwritten
// on the next run with no preview and no refusal.
func TestBaselineOverwritesAHandEditWithoutPreview(t *testing.T) {
	ws := t.TempDir()
	if out, ok := run(t, ws, "the quick brown fox"); !ok {
		t.Fatal(out)
	}
	if err := os.WriteFile(filepath.Join(ws, "notes.txt"), []byte("edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := run(t, ws, "the quick brown fox")
	want(t, out, "notes: wrote notes.txt", "shout: current", "count: current")
	if got := read(t, filepath.Join(ws, "notes.txt")); got != "the quick brown fox\n" {
		t.Errorf("notes.txt = %q", got)
	}
}
