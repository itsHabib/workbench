package e2e_test

import (
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapters"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/apply"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/evidence"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/intent"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/plan"
)

// link exists only in this test file: a symlink resource added without
// touching the compiler, planner, executor or even the builtin list. It is
// composed into a registry the way cmd/wb composes adapters.Builtin().
type link struct{}

func (link) Type() string       { return "link" }
func (link) Kind() adapter.Kind { return adapter.Resource }
func (link) Fields() []adapter.Field {
	return []adapter.Field{{Name: "path", Type: adapter.String, Required: true}, {Name: "target", Type: adapter.String, Required: true}}
}

func (link) Resolve(cfg adapter.Values) (adapter.Values, error) {
	p, err := adapter.CleanPath(cfg.Str("path"))
	if err != nil {
		return nil, &adapter.FieldError{Field: "path", Msg: err.Error()}
	}
	return adapter.Values{"path": p, "target": cfg.Str("target")}, nil
}

func (link) Paths(v adapter.Values) adapter.Paths {
	return adapter.Paths{Owns: []string{v.Str("path")}}
}

func (link) Want(v adapter.Values) adapter.Fact {
	return adapter.Fact{Exists: true, Fingerprint: "link " + v.Str("target"), Summary: "symlink to " + v.Str("target")}
}

func (link) Observe(ws adapter.Workspace, v adapter.Values) (adapter.Fact, error) {
	abs, err := ws.Abs(v.Str("path"))
	if err != nil {
		return adapter.Fact{}, err
	}
	target, err := os.Readlink(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return adapter.Fact{Fingerprint: adapter.Absent}, nil
	}
	if err != nil {
		return adapter.Fact{}, adapter.RelError(v.Str("path"), err)
	}
	return adapter.Fact{Exists: true, Fingerprint: "link " + target, Summary: "symlink to " + target}, nil
}

func (link) Apply(ws adapter.Workspace, v adapter.Values) error {
	abs, err := ws.Abs(v.Str("path"))
	if err != nil {
		return err
	}
	if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Symlink(v.Str("target"), abs)
}

func TestAdapterAddedOutsideTheEngine(t *testing.T) {
	dir := t.TempDir()
	reg, err := adapter.NewRegistry(append(adapters.Builtin(), link{})...)
	if err != nil {
		t.Fatal(err)
	}
	src := `
resource "link" "latest" {
  path   = "latest.txt"
  target = file.input.path
}

resource "file" "input" {
  path    = "input.txt"
  content = "hi\n"
}
`
	in, _, diags := intent.Compile([]intent.Source{{Name: "main.wb.hcl", Bytes: []byte(src)}}, reg)
	if diags.HasErrors() {
		t.Fatal(diags)
	}
	equal(t, "link deps", strings.Join(in.Node("link.latest").Deps, ","), "file.input")
	equal(t, "first node", in.Nodes[0].Address, "file.input")

	ws, _ := adapter.NewWorkspace(dir)
	jr, _ := evidence.Open(dir)
	env := plan.Env{Registry: reg, Workspace: ws, Journal: jr}
	converge := func() []apply.Result {
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

	equal(t, "first apply", fmt.Sprint(outcomes(converge())), "[created created]")
	target, err := os.Readlink(filepath.Join(dir, "latest.txt"))
	equal(t, "link target", target, "input.txt")
	equal(t, "readlink error", err, nil)
	equal(t, "second apply", fmt.Sprint(outcomes(converge())), "[unchanged unchanged]")

	// Hand-retargeting is drift, observed and corrected like any resource.
	_ = os.Remove(filepath.Join(dir, "latest.txt"))
	if err := os.Symlink("elsewhere", filepath.Join(dir, "latest.txt")); err != nil {
		t.Fatal(err)
	}
	equal(t, "after drift", fmt.Sprint(outcomes(converge())), "[unchanged updated]")
}

func outcomes(rs []apply.Result) []apply.Outcome {
	out := make([]apply.Outcome, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Outcome)
	}
	return out
}

// The engine (internal/...) depends on the adapter contract only, never on
// the builtin adapters: it cannot know what a file, an exec or a dir is.
// This is what keeps "add an adapter" free of parser and planner edits.
func TestEngineNeverImportsBuiltinAdapters(t *testing.T) {
	const builtins = "github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapters"
	err := filepath.WalkDir(filepath.Join("..", "internal"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == builtins {
				t.Errorf("%s imports %s", path, builtins)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
