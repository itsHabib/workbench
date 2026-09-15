package apply_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapters"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/apply"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/evidence"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/intent"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/plan"
)

func setup(t *testing.T, src string, extra ...adapter.Adapter) (string, *intent.Intent, plan.Env) {
	t.Helper()
	dir := t.TempDir()
	reg, err := adapter.NewRegistry(append(adapters.Builtin(), extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	in, _, diags := intent.Compile([]intent.Source{{Name: "main.wb.hcl", Bytes: []byte(src)}}, reg)
	if diags.HasErrors() {
		t.Fatal(diags)
	}
	ws, _ := adapter.NewWorkspace(dir)
	jr, _ := evidence.Open(dir)
	return dir, in, plan.Env{Registry: reg, Workspace: ws, Journal: jr}
}

func run(t *testing.T, in *intent.Intent, env plan.Env) []apply.Result {
	t.Helper()
	p, err := plan.Make(in, env)
	if err != nil {
		t.Fatal(err)
	}
	results, err := apply.Apply(context.Background(), p, in, env, apply.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return results
}

// The saved-plan check runs before any effect; this guards the window after
// it. A file changed between planning and its own step fails that step
// instead of being overwritten, and unrelated steps still apply.
func TestChangeDuringApplyFailsStepInsteadOfOverwriting(t *testing.T) {
	dir, in, env := setup(t, `
resource "file" "input" {
  path    = "input.txt"
  content = "desired\n"
}

resource "file" "other" {
  path    = "other.txt"
  content = "unrelated\n"
}
`)
	p, err := plan.Make(in, env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "input.txt"), []byte("someone else\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	results, err := apply.Apply(context.Background(), p, in, env, apply.Options{})
	if err != nil {
		t.Fatal(err)
	}

	if results[0].Outcome != apply.Failed || !strings.Contains(results[0].Note, "changed since plan") {
		t.Errorf("file.input: %+v", results[0])
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "input.txt")); string(b) != "someone else\n" {
		t.Errorf("input.txt overwritten: %q", b)
	}
	if results[1].Outcome != apply.Created {
		t.Errorf("file.other: %+v", results[1])
	}
}

// lazy is a task adapter that "succeeds" without writing its output.
type lazy struct{}

func (lazy) Type() string       { return "lazy" }
func (lazy) Kind() adapter.Kind { return adapter.Task }
func (lazy) Fields() []adapter.Field {
	return []adapter.Field{{Name: "out", Type: adapter.String, Required: true}}
}
func (lazy) Resolve(cfg adapter.Values) (adapter.Values, error) { return cfg, nil }
func (lazy) Paths(v adapter.Values) adapter.Paths {
	return adapter.Paths{Owns: []string{v.Str("out")}}
}
func (lazy) Run(context.Context, adapter.Workspace, adapter.Values) error { return nil }

// F10: a task whose run leaves a declared output absent has failed; it must
// not record a receipt that makes the missing output look converged.
func TestTaskThatSkipsItsDeclaredOutputFails(t *testing.T) {
	_, in, env := setup(t, "task \"lazy\" \"l\" {\n  out = \"never.txt\"\n}\n", lazy{})

	results := run(t, in, env)
	if results[0].Outcome != apply.Failed || !strings.Contains(results[0].Note, "without writing its declared output never.txt") {
		t.Fatalf("first apply: %+v", results[0])
	}
	if again := run(t, in, env); again[0].Outcome != apply.Failed {
		t.Errorf("second apply should retry and fail again: %+v", again[0])
	}
}
