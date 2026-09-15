package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/foundation"
)

// wbBin is the real wb binary, built once. Every end-to-end test drives it
// as a separate process, so exit codes and output are what a user sees.
var wbBin string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "wb-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	wbBin = filepath.Join(dir, "wb")
	if out, err := exec.Command("go", "build", "-o", wbBin, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building wb: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	stdout, stderr string
	code           int
}

// fixture is one freshly created disposable directory: a workspace, a
// source file beside it, and calls.log, which the test commands append to.
// The log sits outside the workspace, so tests count real child processes
// without trusting wb's own journal.
type fixture struct {
	t     *testing.T
	root  string
	ws    string
	src   string
	calls string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := t.TempDir()
	f := &fixture{t: t, root: root, ws: filepath.Join(root, "ws"), src: filepath.Join(root, "pipeline.wb"), calls: filepath.Join(root, "calls.log")}
	f.must(os.Mkdir(f.ws, 0o755))
	return f
}

// pipeline is the common workload, instrumented: each command records that
// it ran before doing its real work.
const pipeline = `keep file notes {
  path "notes.txt"
  text %q
}

run command shout {
  stdin  notes.path
  argv   "sh" "-c" "echo shout >> '%[2]s'; tr a-z A-Z"
  stdout "notes.upper.txt"
}

run command count {
  stdin  notes.path
  argv   "sh" "-c" "echo count >> '%[2]s'; awk '{ words += NF } END { print words }'"
  stdout "notes.count.txt"
}
`

func (f *fixture) pipeline(text string) { f.source(fmt.Sprintf(pipeline, text, f.calls)) }

func (f *fixture) source(src string) { f.must(os.WriteFile(f.src, []byte(src), 0o644)) }

func (f *fixture) must(err error) {
	f.t.Helper()
	if err != nil {
		f.t.Fatal(err)
	}
}

// wbEnv runs wb in the disposable directory with extra environment.
func (f *fixture) wbEnv(env []string, args ...string) result {
	f.t.Helper()
	cmd := exec.Command(wbBin, args...)
	cmd.Dir = f.root
	cmd.Env = append(os.Environ(), env...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		f.t.Fatal(err)
	}
	code := 0
	if exit != nil {
		code = exit.ExitCode()
	}
	return result{stdout: out.String(), stderr: errOut.String(), code: code}
}

func (f *fixture) wb(args ...string) result { f.t.Helper(); return f.wbEnv(nil, args...) }

func (f *fixture) plan(extra ...string) result {
	f.t.Helper()
	return f.wb(append(append([]string{"plan", "-dir", "ws"}, extra...), "pipeline.wb")...)
}

func (f *fixture) apply() result { f.t.Helper(); return f.wb("apply", "-dir", "ws", "pipeline.wb") }

// mustApply applies and fails the test unless apply succeeds.
func (f *fixture) mustApply() result {
	f.t.Helper()
	r := f.apply()
	r.want(f.t, 0)
	return r
}

// read returns a workspace file's content, or "<absent>".
func (f *fixture) read(rel string) string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.ws, rel))
	if errors.Is(err, fs.ErrNotExist) {
		return "<absent>"
	}
	f.must(err)
	return string(b)
}

func (f *fixture) write(rel, content string) {
	f.t.Helper()
	f.must(os.WriteFile(filepath.Join(f.ws, rel), []byte(content), 0o644))
}

// calledTimes counts real child-process invocations per command.
func (f *fixture) calledTimes() map[string]int {
	f.t.Helper()
	n := map[string]int{}
	b, err := os.ReadFile(f.calls)
	if errors.Is(err, fs.ErrNotExist) {
		return n
	}
	f.must(err)
	for _, line := range strings.Fields(string(b)) {
		n[line]++
	}
	return n
}

// journal parses the workspace journal as the foundation wrote it.
func (f *fixture) journal() []foundation.Record {
	f.t.Helper()
	file, err := os.Open(filepath.Join(f.ws, foundation.JournalPath))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	f.must(err)
	defer file.Close()
	var recs []foundation.Record
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		var r foundation.Record
		f.must(json.Unmarshal(sc.Bytes(), &r))
		recs = append(recs, r)
	}
	f.must(sc.Err())
	return recs
}

// ops lists "op id" for each journal record, in order.
func (f *fixture) ops() []string {
	var out []string
	for _, r := range f.journal() {
		out = append(out, r.Op+" "+r.ID)
	}
	return out
}

// snapshot is everything an unintended effect would change: file contents
// and modification times in the workspace, the journal, and the calls log.
type snapshot struct {
	files   map[string]string
	mtimes  map[string]time.Time
	journal int
	calls   map[string]int
}

func (f *fixture) snapshot() snapshot {
	f.t.Helper()
	s := snapshot{files: map[string]string{}, mtimes: map[string]time.Time{}, journal: len(f.journal()), calls: f.calledTimes()}
	entries, err := os.ReadDir(f.ws)
	f.must(err)
	for _, e := range entries {
		info, err := e.Info()
		f.must(err)
		s.mtimes[e.Name()] = info.ModTime()
		if e.Type().IsRegular() {
			s.files[e.Name()] = f.read(e.Name())
		}
	}
	return s
}

func (f *fixture) unchangedSince(before snapshot) {
	f.t.Helper()
	if after := f.snapshot(); !reflect.DeepEqual(before, after) {
		f.t.Errorf("workspace changed:\nbefore %+v\nafter  %+v", before, after)
	}
}

func (r result) want(t *testing.T, code int) {
	t.Helper()
	if r.code != code {
		t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", r.code, code, r.stdout, r.stderr)
	}
}

func contains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q; got:\n%s", w, got)
		}
	}
}

func equal[T any](t *testing.T, what string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}
