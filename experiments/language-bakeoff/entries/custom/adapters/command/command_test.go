package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/intent"
)

func workspace(t *testing.T) (*foundation.Workspace, string, string) {
	t.Helper()
	root := t.TempDir()
	dir, outside := filepath.Join(root, "ws"), filepath.Join(root, "outside")
	for _, d := range []string{dir, outside} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := foundation.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ws.Close)
	return ws, dir, outside
}

func decl(attrs map[string][]string) intent.Decl {
	return intent.Decl{ID: "c", Lifecycle: "run", Kind: "command", Attrs: attrs}
}

// The planner digests inputs through os.Root too, but the adapter must hold
// the line on its own: a working directory that leaves the workspace is
// refused before the child starts.
func TestRunRefusesAWorkingDirectoryOutsideTheWorkspace(t *testing.T) {
	ws, dir, outside := workspace(t)
	if err := os.Symlink(outside, filepath.Join(dir, "away")); err != nil {
		t.Fatal(err)
	}
	_, err := Adapter{}.Run(context.Background(), ws, decl(map[string][]string{
		"cwd": {"away"}, "argv": {"sh", "-c", "echo escaped > escaped.txt"}, "stdout": {"o.txt"},
	}))
	if err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Fatalf("err = %v, want the escape refused", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("the child ran outside the workspace: %v", entries)
	}
}

func TestRunCreatesTheOutputDirectory(t *testing.T) {
	ws, dir, _ := workspace(t)
	if _, err := (Adapter{}).Run(context.Background(), ws, decl(map[string][]string{
		"argv": {"sh", "-c", "echo hi"}, "stdout": {"out/gen.txt"},
	})); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "out", "gen.txt")); err != nil || string(b) != "hi\n" {
		t.Errorf("out/gen.txt = %q, %v", b, err)
	}
}

func TestRunRejectsAnEmptyArgv(t *testing.T) {
	ws, _, _ := workspace(t)
	_, err := Adapter{}.Run(context.Background(), ws, decl(map[string][]string{"stdout": {"o.txt"}}))
	if err == nil || !strings.Contains(err.Error(), "argv is empty") {
		t.Fatalf("err = %v", err)
	}
}

func TestFailedRunLeavesThePreviousOutputAndNoTemporaryFile(t *testing.T) {
	ws, dir, _ := workspace(t)
	if err := os.WriteFile(filepath.Join(dir, "o.txt"), []byte("previous\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Adapter{}.Run(context.Background(), ws, decl(map[string][]string{
		"argv": {"sh", "-c", "echo partial; echo broken >&2; exit 3"}, "stdout": {"o.txt"},
	}))
	if err == nil || err.Error() != "sh: exit status 3: broken" {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "o.txt")); string(b) != "previous\n" {
		t.Errorf("o.txt = %q, want the previous output kept", b)
	}
	if _, err := os.Lstat(filepath.Join(dir, "o.txt"+foundation.TempSuffix)); !os.IsNotExist(err) {
		t.Errorf("temporary file left behind: %v", err)
	}
}
