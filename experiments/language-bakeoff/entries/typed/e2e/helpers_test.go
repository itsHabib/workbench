// Package e2e drives the compiled workflow programs the way an operator
// would, in fresh temporary directories, and asserts on exit codes, file
// contents, file identity and evidence, not only on printed output.
package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/engine"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/wb"
)

var update = flag.Bool("update", false, "rewrite the golden examples in docs/examples")

var bin string

var programs = []string{"textpipe", "scratchpipe"}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "wb-e2e-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, p := range programs {
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, p), "github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/cmd/"+p)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "build", p, err)
			os.Exit(1)
		}
	}
	bin = dir
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// Texts used across cases, with their expected transform outputs.
const (
	text0 = "the quick brown fox\njumps over the lazy dog\n"
	text3 = "the quick brown fox\njumps over the lazy dog\nthen naps\n"
	text4 = "hello typed workbench\n"
	text5 = "retry me please\nnow\n"
)

// sandbox is one fresh disposable directory: ws/ is the workspace and
// plans/ holds saved plans. Commands run from the sandbox root, so every
// path they print is relative and output is reproducible.
type sandbox struct {
	t    *testing.T
	root string
}

func newSandbox(t *testing.T) sandbox {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"ws", "plans"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return sandbox{t: t, root: root}
}

type result struct {
	code        int
	stdout      string
	stderr      string
	killedBySig bool
}

// run executes a workflow program with a clean environment plus env.
func (s sandbox) run(env []string, program string, args ...string) result {
	s.t.Helper()
	cmd := exec.Command(filepath.Join(bin, program), args...)
	cmd.Dir = s.root
	cmd.Env = append(cleanEnv(), env...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	r := result{stdout: out.String(), stderr: errb.String()}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		r.code = ee.ExitCode()
		r.killedBySig = ee.ExitCode() == -1
		return r
	}
	if err != nil {
		s.t.Fatalf("%s %v: %v", program, args, err)
	}
	return r
}

// must runs a program and fails the test unless it exits with want.
func (s sandbox) must(want int, env []string, program string, args ...string) result {
	s.t.Helper()
	r := s.run(env, program, args...)
	if r.code != want {
		s.t.Fatalf("%s %s: exit %d, want %d\nstdout:\n%s\nstderr:\n%s",
			program, strings.Join(args, " "), r.code, want, r.stdout, r.stderr)
	}
	return r
}

func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "WB_FAULT=") {
			env = append(env, kv)
		}
	}
	return env
}

func (s sandbox) path(rel string) string { return filepath.Join(s.root, rel) }

func (s sandbox) read(rel string) string {
	s.t.Helper()
	b, err := os.ReadFile(s.path(rel))
	if err != nil {
		s.t.Fatal(err)
	}
	return string(b)
}

func (s sandbox) write(rel, content string) {
	s.t.Helper()
	if err := os.WriteFile(s.path(rel), []byte(content), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

func (s sandbox) stat(rel string) os.FileInfo {
	s.t.Helper()
	info, err := os.Stat(s.path(rel))
	if err != nil {
		s.t.Fatal(err)
	}
	return info
}

func (s sandbox) plan(rel string) engine.Plan {
	s.t.Helper()
	var p engine.Plan
	if err := json.Unmarshal([]byte(s.read(rel)), &p); err != nil {
		s.t.Fatal(err)
	}
	return p
}

func (s sandbox) evidence() []foundation.Record {
	s.t.Helper()
	b, err := os.ReadFile(s.path("ws/.wb/evidence.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		s.t.Fatal(err)
	}
	var recs []foundation.Record
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var r foundation.Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			s.t.Fatalf("evidence line %q: %v", line, err)
		}
		recs = append(recs, r)
	}
	return recs
}

// events renders evidence compactly: "start transform.upper", "finish transform.upper ok".
func (s sandbox) events() []string {
	var out []string
	for _, r := range s.evidence() {
		out = append(out, strings.TrimSpace(strings.Join([]string{r.Event, r.Subject, r.Status}, " ")))
	}
	return out
}

func (s sandbox) starts(subject string) int {
	n := 0
	for _, r := range s.evidence() {
		if r.Event == foundation.EventStart && r.Subject == subject {
			n++
		}
	}
	return n
}

// files records file identities; a rewrite (temp file + rename) changes them.
type files map[string]os.FileInfo

func (s sandbox) identities(rels ...string) files {
	f := files{}
	for _, r := range rels {
		f[r] = s.stat(r)
	}
	return f
}

func (s sandbox) assertNotRewritten(before files) {
	s.t.Helper()
	for rel, info := range before {
		if !os.SameFile(info, s.stat(rel)) {
			s.t.Errorf("%s was rewritten", rel)
		}
	}
}

func assertOps(t *testing.T, p engine.Plan, want map[wb.Address]engine.Op) {
	t.Helper()
	got := map[wb.Address]engine.Op{}
	for _, s := range p.Steps {
		got[s.Address] = s.Op
	}
	for a, op := range want {
		if got[a] != op {
			t.Errorf("%s: op %q, want %q", a, got[a], op)
		}
	}
	if len(got) != len(want) {
		t.Errorf("plan has %d steps, want %d: %v", len(got), len(want), got)
	}
}

func stepOf(p engine.Plan, a wb.Address) engine.Step {
	for _, s := range p.Steps {
		if s.Address == a {
			return s
		}
	}
	return engine.Step{}
}

func mentions(reasons []string, sub string) bool {
	for _, r := range reasons {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}

// golden compares output with docs/examples/<name>, or rewrites it with -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	file := filepath.Join("..", "docs", "examples", name)
	if *update {
		if err := os.WriteFile(file, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("%v (run go test ./e2e -update to create it)", err)
	}
	if string(want) != got {
		t.Errorf("output differs from %s (go test ./e2e -update to accept):\n--- want\n%s\n--- got\n%s", file, want, got)
	}
}
