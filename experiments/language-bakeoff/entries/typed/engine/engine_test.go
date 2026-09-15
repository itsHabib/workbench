package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/adapters/file"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/adapters/transform"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/engine"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/wb"
)

var ctx = context.Background()

type fixture struct {
	t   *testing.T
	ws  *foundation.Workspace
	reg engine.Registry
}

func newFixture(t *testing.T, fault foundation.Fault, extra ...engine.Adapter) fixture {
	t.Helper()
	ws, err := foundation.Open(t.TempDir(), fault)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	reg, err := engine.NewRegistry(append([]engine.Adapter{file.Adapter{}, transform.Adapter{}}, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{t: t, ws: ws, reg: reg}
}

func (f fixture) plan(define wb.Define, vars map[string]string) engine.Plan {
	f.t.Helper()
	in, err := wb.Evaluate(define, vars)
	if err != nil {
		f.t.Fatal(err)
	}
	p, err := engine.Compute(f.ws, f.reg, in)
	if err != nil {
		f.t.Fatal(err)
	}
	return p
}

func (f fixture) apply(p engine.Plan) map[wb.Address]engine.Result {
	f.t.Helper()
	results, err := engine.Apply(ctx, f.ws, f.reg, p)
	if err != nil {
		f.t.Fatal(err)
	}
	out := map[wb.Address]engine.Result{}
	for _, r := range results {
		out[r.Step.Address] = r
	}
	return out
}

func (f fixture) read(p string) string {
	f.t.Helper()
	st, err := f.ws.ReadFile(p)
	if err != nil {
		f.t.Fatal(err)
	}
	return string(st.Content)
}

func (f fixture) starts(subject string) int {
	f.t.Helper()
	ev, err := f.ws.Evidence()
	if err != nil {
		f.t.Fatal(err)
	}
	n := 0
	for _, r := range ev.Records {
		if r.Subject == subject && r.Event == foundation.EventStart {
			n++
		}
	}
	return n
}

func ops(p engine.Plan) map[wb.Address]engine.Op {
	out := map[wb.Address]engine.Op{}
	for _, s := range p.Steps {
		out[s.Address] = s.Op
	}
	return out
}

func step(p engine.Plan, a wb.Address) engine.Step {
	for _, s := range p.Steps {
		if s.Address == a {
			return s
		}
	}
	return engine.Step{}
}

// chain: input -> upper -> shout (reads upper's output), and input -> words.
func chain(p *wb.Params) *wb.Graph {
	g := wb.NewGraph("chain")
	in := file.New(g, "input", file.Spec{Path: "in.txt", Content: p.String("text", "a b\n")})
	up := transform.New(g, "upper", transform.Spec{Command: []string{"tr", "a-z", "A-Z"}, Stdin: in, Output: wb.Rel("up.txt")})
	transform.New(g, "shout", transform.Spec{Command: []string{"sed", "s/$/!/"}, Stdin: up, Output: wb.Rel("shout.txt")})
	transform.New(g, "words", transform.Spec{Command: []string{"wc", "-w"}, Stdin: in, Output: wb.Rel("words.txt")})
	return g
}

func TestDownstreamOfARunningTaskIsKnownOnlyAfterApply(t *testing.T) {
	f := newFixture(t, foundation.Fault{})
	f.apply(f.plan(chain, nil))

	p := f.plan(chain, map[string]string{"text": "c d\n"})
	shout := step(p, "transform.shout")
	if shout.Op != engine.Run || shout.Inputs["transform.upper"] != engine.KnownAfterApply {
		t.Fatalf("shout step = %+v; want run with upper's digest unknown", shout)
	}
	if !slices.ContainsFunc(shout.Reasons, func(r string) bool { return strings.Contains(r, "runs first") }) {
		t.Errorf("reasons %q do not explain the upstream run", shout.Reasons)
	}
	f.apply(p)
	if got := f.read("shout.txt"); got != "C D!\n" {
		t.Errorf("shout.txt = %q", got)
	}
	if got := ops(f.plan(chain, map[string]string{"text": "c d\n"})); got["transform.shout"] != engine.None {
		t.Errorf("after apply, plan = %v; want converged", got)
	}
}

func TestAFailureSkipsDependentsAndSparesUnrelatedWork(t *testing.T) {
	f := newFixture(t, foundation.Fault{Fail: "transform.upper"})
	got := f.apply(f.plan(chain, nil))
	want := map[wb.Address]engine.Status{
		"file.input": engine.Done, "transform.upper": engine.Failed,
		"transform.shout": engine.Skipped, "transform.words": engine.Done,
	}
	for a, s := range want {
		if got[a].Status != s {
			t.Errorf("%s: %s (%s), want %s", a, got[a].Status, got[a].Detail, s)
		}
	}

	// Retry without the fault: only the failed task and its dependent run.
	f = fixture{t: t, ws: reopen(t, f.ws), reg: f.reg}
	p := f.plan(chain, nil)
	wantOps := map[wb.Address]engine.Op{
		"file.input": engine.None, "transform.upper": engine.Run,
		"transform.shout": engine.Run, "transform.words": engine.None,
	}
	if o := ops(p); !maps.Equal(o, wantOps) {
		t.Fatalf("retry plan = %v, want %v", o, wantOps)
	}
	f.apply(p)
	if f.starts("transform.words") != 1 {
		t.Errorf("transform.words ran %d times, want 1", f.starts("transform.words"))
	}
	if got := f.read("shout.txt"); got != "A B!\n" {
		t.Errorf("shout.txt = %q", got)
	}
}

// single: one input and one task whose command is a param, so a test can
// change the definition without changing any input.
func single(p *wb.Params) *wb.Graph {
	g := wb.NewGraph("single")
	in := file.New(g, "input", file.Spec{Path: "in.txt", Content: "a b\n"})
	transform.New(g, "upper", transform.Spec{Command: []string{"tr", "a-z", p.String("to", "A-Z")}, Stdin: in, Output: wb.Rel("up.txt")})
	return g
}

func TestATaskReRunsWhenItsResultIsStale(t *testing.T) {
	cases := map[string]struct {
		change func(f fixture)
		vars   map[string]string
		reason string
	}{
		"output edited":      {func(f fixture) { _ = f.ws.WriteFile("up.txt", []byte("edited\n")) }, nil, "changed outside the workflow"},
		"output deleted":     {func(f fixture) { _ = f.ws.RemoveAll("up.txt") }, nil, "is missing"},
		"definition changed": {func(fixture) {}, map[string]string{"to": "B-Z"}, "definition changed since run"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, foundation.Fault{})
			f.apply(f.plan(single, nil))
			c.change(f)
			p := f.plan(single, c.vars)
			up := step(p, "transform.upper")
			if up.Op != engine.Run || !slices.ContainsFunc(up.Reasons, func(r string) bool { return strings.Contains(r, c.reason) }) {
				t.Fatalf("upper = %s %q, want run because %q", up.Op, up.Reasons, c.reason)
			}
			if step(p, "file.input").Op != engine.None {
				t.Error("the input should be untouched")
			}
			f.apply(p)
			if got := ops(f.plan(single, c.vars)); got["transform.upper"] != engine.None {
				t.Errorf("not converged after re-run: %v", got)
			}
		})
	}
}

func TestAStalePlanAppliesNothing(t *testing.T) {
	cases := map[string]func(f fixture){
		"input edited":  func(f fixture) { _ = f.ws.WriteFile("in.txt", []byte("edited\n")) },
		"output edited": func(f fixture) { _ = f.ws.WriteFile("up.txt", []byte("edited\n")) },
		"another apply ran": func(f fixture) {
			_, _ = f.ws.Record(foundation.Record{Event: foundation.EventApply, Subject: "someone-else"})
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, foundation.Fault{})
			f.apply(f.plan(chain, nil))
			p := f.plan(chain, map[string]string{"text": "new\n"})
			change(f)
			before, _ := f.ws.Evidence()

			_, err := engine.Apply(ctx, f.ws, f.reg, p)
			var stale *engine.StaleError
			if !errors.As(err, &stale) {
				t.Fatalf("Apply error = %v, want StaleError", err)
			}
			after, _ := f.ws.Evidence()
			if after.Head() != before.Head() {
				t.Errorf("stale apply wrote evidence #%d..#%d", before.Head()+1, after.Head())
			}
			if f.read("in.txt") == "new\n" {
				t.Error("stale apply wrote the input")
			}
		})
	}
}

// tamper is a test resource whose apply also writes its spec's target,
// standing in for another actor editing a file while an apply runs.
type tamper struct{}

func (tamper) Kind() string { return "tamper" }

func (tamper) Plan(ws *foundation.Workspace, _ wb.Node) (engine.Change, error) {
	st, err := ws.ReadFile("tamper.txt")
	if err != nil || st.Exists {
		return engine.Change{Op: engine.None, Before: engine.State{Exists: true, Digest: st.Digest},
			After: engine.State{Exists: true, Digest: st.Digest}}, err
	}
	return engine.Change{Op: engine.Create, After: engine.State{Exists: true, Digest: foundation.Digest([]byte("t"))}}, nil
}

func (tamper) Apply(ws *foundation.Workspace, n wb.Node, _ engine.Change) error {
	var target string
	if err := json.Unmarshal(n.Spec, &target); err != nil {
		return err
	}
	if err := ws.WriteFile(target, []byte("tampered\n")); err != nil {
		return err
	}
	return ws.WriteFile("tamper.txt", []byte("t"))
}

func declareTamper(g *wb.Graph, target string) {
	g.Declare(wb.Decl{Kind: "tamper", Name: "t", Class: wb.Resource, Owns: wb.Rel("tamper.txt"), Spec: target})
}

func TestAResourceChangedDuringApplyIsNotOverwritten(t *testing.T) {
	f := newFixture(t, foundation.Fault{}, tamper{})
	define := func(*wb.Params) *wb.Graph {
		g := wb.NewGraph("tampered")
		declareTamper(g, "b.txt")
		file.New(g, "b", file.Spec{Path: "b.txt", Content: "declared\n"})
		return g
	}
	got := f.apply(f.plan(define, nil))
	if b := got["file.b"]; b.Status != engine.Failed || !strings.Contains(b.Detail, "changed during apply") {
		t.Fatalf("file.b = %s %q, want failure naming the mid-apply change", b.Status, b.Detail)
	}
	if got := f.read("b.txt"); got != "tampered\n" {
		t.Errorf("b.txt = %q; the concurrent write must not be overwritten", got)
	}
}

func TestATaskRefusesInputsThatChangedDuringApply(t *testing.T) {
	f := newFixture(t, foundation.Fault{}, tamper{})
	define := func(*wb.Params) *wb.Graph {
		g := wb.NewGraph("tampered")
		in := file.New(g, "input", file.Spec{Path: "in.txt", Content: "a\n"})
		declareTamper(g, "in.txt")
		transform.New(g, "upper", transform.Spec{Command: []string{"tr", "a-z", "A-Z"}, Stdin: in, Output: wb.Rel("up.txt")})
		return g
	}
	got := f.apply(f.plan(define, nil))
	up := got["transform.upper"]
	if up.Status != engine.Failed || !strings.Contains(up.Detail, "changed during apply") {
		t.Fatalf("upper = %s %q, want failure naming the mid-apply change", up.Status, up.Detail)
	}
	if f.starts("transform.upper") != 0 {
		t.Error("the task started on inputs the plan did not predict")
	}
}

func TestRegistryMismatchIsAPlanError(t *testing.T) {
	f := newFixture(t, foundation.Fault{})
	define := func(*wb.Params) *wb.Graph {
		g := wb.NewGraph("odd")
		g.Declare(wb.Decl{Kind: "file", Name: "x", Class: wb.Task, Owns: wb.Rel("x"), Spec: file.Spec{Path: "x"}})
		g.Declare(wb.Decl{Kind: "nosuch", Name: "y", Class: wb.Resource, Owns: wb.Rel("y"), Spec: 1})
		return g
	}
	in, err := wb.Evaluate(define, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Compute(f.ws, f.reg, in); err == nil || !strings.Contains(err.Error(), "does not serve task nodes") {
		t.Fatalf("err = %v, want class mismatch", err)
	}
}

// A plan this program cannot apply is an error (exit 1), not "stale"
// (exit 3): the workspace did not change, the plan or the program did.
func TestAnUnappliablePlanIsAnErrorNotStale(t *testing.T) {
	f := newFixture(t, foundation.Fault{})
	p := f.plan(single, nil)
	edits := map[string]func(p *engine.Plan){
		"steps reordered": func(p *engine.Plan) { p.Steps[0], p.Steps[1] = p.Steps[1], p.Steps[0] },
		"intent edited, digest recomputed": func(p *engine.Plan) {
			p.Intent.Nodes[0].Owns = "other.txt"
			p.IntentDigest = p.Intent.Digest()
		},
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			bad := p
			bad.Steps = slices.Clone(p.Steps)
			bad.Intent.Nodes = slices.Clone(p.Intent.Nodes)
			edit(&bad)
			_, err := engine.Apply(ctx, f.ws, f.reg, bad)
			var stale *engine.StaleError
			if err == nil || errors.As(err, &stale) {
				t.Fatalf("err = %v, want a non-stale refusal", err)
			}
		})
	}
	// A spec path that differs from the owned path is refused before any
	// effect, even with a valid intent and a recomputed digest.
	moved := p
	moved.Intent.Nodes = slices.Clone(p.Intent.Nodes)
	moved.Intent.Nodes[0].Spec = json.RawMessage(`{"path":"elsewhere.txt","content":"x"}`)
	moved.IntentDigest = moved.Intent.Digest()
	if _, err := engine.Apply(ctx, f.ws, f.reg, moved); err == nil || !strings.Contains(err.Error(), "not the owned path") {
		t.Fatalf("err = %v, want the owned-path refusal", err)
	}
	if st, _ := f.ws.ReadFile("elsewhere.txt"); st.Exists {
		t.Error("an adapter wrote a path its node does not own")
	}

	other, err := engine.NewRegistry(file.Adapter{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Apply(ctx, f.ws, other, p); err == nil || !strings.Contains(err.Error(), "no adapter") {
		t.Fatalf("err = %v, want missing adapter", err)
	}
}

// A saved step whose op was edited by hand is refused at its effect: the
// adapter must still propose exactly the reviewed change.
func TestAnEditedStepOpIsRefused(t *testing.T) {
	f := newFixture(t, foundation.Fault{})
	f.apply(f.plan(single, nil))
	p := f.plan(single, nil)
	p.Steps = slices.Clone(p.Steps)
	p.Steps[0].Op = engine.Replace
	got := f.apply(p)
	if in := got["file.input"]; in.Status != engine.Failed || !strings.Contains(in.Detail, "the plan says replace") {
		t.Fatalf("file.input = %s %q, want refusal of the edited op", in.Status, in.Detail)
	}
}

func reopen(t *testing.T, ws *foundation.Workspace) *foundation.Workspace {
	t.Helper()
	dir := ws.Dir()
	_ = ws.Close()
	fresh, err := foundation.Open(dir, foundation.Fault{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	return fresh
}
