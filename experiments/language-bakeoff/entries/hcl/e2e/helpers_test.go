// Package e2e_test drives the real wb binary through the six common cases
// in fresh disposable directories and asserts on files, inode identities,
// journal entries and an independent witness of child-process runs.
package e2e_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/evidence"
)

var wbBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "wb-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	wbBin = filepath.Join(dir, "wb")
	code := 1
	out, err := exec.Command("go", "build", "-o", wbBin, "../cmd/wb").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build wb: %v\n%s", err, out)
	}
	if err == nil {
		code = m.Run()
	}
	_ = os.RemoveAll(dir) // best-effort cleanup of the test binary
	os.Exit(code)
}

// workspace is a fresh disposable directory plus a `tr` wrapper placed
// first on PATH. The wrapper appends to a witness file outside the
// workspace on every run, so tests count real child-process executions
// without trusting wb's own journal, and it fails on demand.
type workspace struct {
	t        *testing.T
	dir      string
	path     string
	witness  string
	failFlag string
}

func newWorkspace(t *testing.T) *workspace {
	t.Helper()
	tools := t.TempDir()
	w := &workspace{
		t:        t,
		dir:      t.TempDir(),
		witness:  filepath.Join(tools, "tr-runs.log"),
		failFlag: filepath.Join(tools, "tr-fail"),
	}
	realTr, err := exec.LookPath("tr")
	if err != nil {
		t.Fatalf("find tr: %v", err)
	}
	script := fmt.Sprintf("#!/bin/sh\nif [ -e '%s' ]; then echo 'tr: injected failure' >&2; exit 3; fi\necho run >> '%s'\nexec '%s' \"$@\"\n",
		w.failFlag, w.witness, realTr)
	if err := os.WriteFile(filepath.Join(tools, "tr"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	w.path = tools + string(os.PathListSeparator) + os.Getenv("PATH")
	return w
}

// applied returns a workspace where example has been applied successfully.
func applied(t *testing.T, example string) *workspace {
	t.Helper()
	w := newWorkspace(t)
	w.use(example)
	w.mustWB("apply")
	return w
}

func (w *workspace) use(example string) {
	w.t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "examples", example))
	if err != nil {
		w.t.Fatal(err)
	}
	w.write("main.wb.hcl", string(b))
}

func (w *workspace) write(rel, content string) {
	w.t.Helper()
	if err := os.WriteFile(filepath.Join(w.dir, rel), []byte(content), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

func (w *workspace) appendTo(rel, content string) {
	w.t.Helper()
	f, err := os.OpenFile(filepath.Join(w.dir, rel), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		w.t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		w.t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		w.t.Fatal(err)
	}
}

func (w *workspace) read(rel string) string {
	w.t.Helper()
	b, err := os.ReadFile(filepath.Join(w.dir, rel))
	if err != nil {
		w.t.Fatal(err)
	}
	return string(b)
}

// identity changes whenever a file is rewritten: atomic replaces allocate a
// new inode, and any in-place write moves the modification time.
func (w *workspace) identity(rel string) string {
	w.t.Helper()
	info, err := os.Stat(filepath.Join(w.dir, rel))
	if err != nil {
		w.t.Fatal(err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		w.t.Fatal("no inode information on this platform")
	}
	return fmt.Sprintf("ino %d mtime %d", st.Ino, info.ModTime().UnixNano())
}

func (w *workspace) identities(rels ...string) map[string]string {
	w.t.Helper()
	out := map[string]string{}
	for _, r := range rels {
		out[r] = w.identity(r)
	}
	return out
}

// runs counts real tr executions recorded by the witness wrapper.
func (w *workspace) runs() int {
	w.t.Helper()
	b, err := os.ReadFile(w.witness)
	if errors.Is(err, os.ErrNotExist) {
		return 0
	}
	if err != nil {
		w.t.Fatal(err)
	}
	return strings.Count(string(b), "run\n")
}

func (w *workspace) failTr(on bool) {
	w.t.Helper()
	if !on {
		w.removeAbs(w.failFlag)
		return
	}
	if err := os.WriteFile(w.failFlag, nil, 0o644); err != nil {
		w.t.Fatal(err)
	}
}

func (w *workspace) remove(rel string) {
	w.t.Helper()
	w.removeAbs(filepath.Join(w.dir, rel))
}

func (w *workspace) removeAbs(path string) {
	w.t.Helper()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		w.t.Fatal(err)
	}
}

func (w *workspace) journal() []evidence.Entry {
	w.t.Helper()
	j, err := evidence.Open(w.dir)
	if err != nil {
		w.t.Fatal(err)
	}
	return j.Entries()
}

func (w *workspace) latest(address string) evidence.Entry {
	w.t.Helper()
	entries := w.journal()
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Address == address {
			return entries[i]
		}
	}
	w.t.Fatalf("no journal entry for %s", address)
	return evidence.Entry{}
}

func (w *workspace) entriesFor(addresses ...string) int {
	w.t.Helper()
	want := map[string]bool{}
	for _, a := range addresses {
		want[a] = true
	}
	n := 0
	for _, e := range w.journal() {
		if want[e.Address] {
			n++
		}
	}
	return n
}

func (w *workspace) listing() []string {
	w.t.Helper()
	ents, err := os.ReadDir(w.dir)
	if err != nil {
		w.t.Fatal(err)
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names
}

type result struct {
	stdout, stderr string
	code           int
}

func (w *workspace) wb(args ...string) result { return w.wbEnv(nil, args...) }

func (w *workspace) wbEnv(env []string, args ...string) result {
	w.t.Helper()
	cmd := exec.Command(wbBin, args...)
	cmd.Dir = w.dir
	cmd.Env = append([]string{"PATH=" + w.path, "HOME=" + w.dir}, env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		w.t.Fatalf("run wb %v: %v", args, err)
	}
	return result{stdout: out.String(), stderr: errb.String(), code: cmd.ProcessState.ExitCode()}
}

func (w *workspace) mustWB(args ...string) result {
	w.t.Helper()
	r := w.wb(args...)
	if r.code != 0 {
		w.t.Fatalf("wb %v: exit %d\nstdout:\n%s\nstderr:\n%s", args, r.code, r.stdout, r.stderr)
	}
	return r
}

func contains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q\n--- output ---\n%s", want, got)
		}
	}
}

func equal[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func sameMap(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s = %q, want %q", what, k, got[k], v)
		}
	}
}
