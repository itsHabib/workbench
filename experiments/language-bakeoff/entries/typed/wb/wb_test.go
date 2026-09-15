package wb

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

type spec struct {
	In  Ref  `json:"in"`
	Out Path `json:"out"`
}

func TestDependenciesComeFromReferencesInTheSpec(t *testing.T) {
	g := NewGraph("t")
	a := g.Declare(Decl{Kind: "src", Name: "a", Class: Resource, Owns: Rel("a.txt"), Spec: map[string]string{}})
	dir := g.Declare(Decl{Kind: "dir", Name: "d", Class: Resource, Owns: Rel("d"), Spec: map[string]string{}})
	g.Declare(Decl{Kind: "job", Name: "j", Class: Task, Owns: dir.Join("out.txt"), Spec: spec{In: a, Out: dir.Join("out.txt")}})
	in, err := g.Intent()
	if err != nil {
		t.Fatal(err)
	}
	j, _ := in.Node("job.j")
	if want := []Address{"dir.d", "src.a"}; !slices.Equal(j.Deps, want) {
		t.Errorf("deps = %v, want %v", j.Deps, want)
	}
	if !strings.Contains(string(j.Spec), `"in":{"$ref":"src.a","path":"a.txt"}`) {
		t.Errorf("spec does not show the reference: %s", j.Spec)
	}
}

// Each invalid declaration is checked on its own graph, so one case's
// message cannot mask another that silently stopped failing.
func TestInvalidDeclarationsAreRejected(t *testing.T) {
	other := NewGraph("other")
	foreign := other.Declare(Decl{Kind: "src", Name: "x", Class: Resource, Owns: Rel("x"), Spec: 1})
	collide := other.Declare(Decl{Kind: "src", Name: "a", Class: Resource, Owns: Rel("secret.txt"), Spec: 1})
	within := NewGraph("within").Declare(Decl{Kind: "src", Name: "a", Class: Resource, Owns: Rel("a/inner.txt"), Spec: 1})

	cases := map[string]struct {
		d    func(a Ref) Decl
		want string
	}{
		"duplicate":       {func(Ref) Decl { return Decl{Kind: "src", Name: "a", Class: Resource, Owns: Rel("b"), Spec: 1} }, "declared twice"},
		"bad name":        {func(Ref) Decl { return Decl{Kind: "src", Name: "Bad", Class: Resource, Owns: Rel("c"), Spec: 1} }, "<kind>.<name>"},
		"bad class":       {func(Ref) Decl { return Decl{Kind: "src", Name: "k", Class: "thing", Owns: Rel("c"), Spec: 1} }, "unknown class"},
		"absolute":        {func(Ref) Decl { return Decl{Kind: "src", Name: "n", Class: Resource, Owns: Rel("/etc/x"), Spec: 1} }, "clean relative path"},
		"escape":          {func(Ref) Decl { return Decl{Kind: "src", Name: "n", Class: Resource, Owns: Rel("../x"), Spec: 1} }, "clean relative path"},
		"root":            {func(Ref) Decl { return Decl{Kind: "src", Name: "n", Class: Resource, Owns: Rel("."), Spec: 1} }, "clean relative path"},
		"evidence":        {func(Ref) Decl { return Decl{Kind: "src", Name: "n", Class: Resource, Owns: Rel(".wb/x"), Spec: 1} }, "reserved"},
		"evidence folded": {func(Ref) Decl { return Decl{Kind: "src", Name: "n", Class: Resource, Owns: Rel(".WB"), Spec: 1} }, "reserved"},
		"engine name": {func(Ref) Decl {
			return Decl{Kind: "src", Name: "n", Class: Resource, Owns: Rel("d/.wb-scratchdir"), Spec: 1}
		}, "reserved"},
		"non-portable": {func(Ref) Decl {
			return Decl{Kind: "src", Name: "n", Class: Resource, Owns: Rel("caf\u00e9.txt"), Spec: 1}
		}, "names may use only"},
		"closure": {func(Ref) Decl { return Decl{Kind: "src", Name: "n", Class: Resource, Owns: Rel("f"), Spec: func() {}} }, "plain data"},
		"foreign ref": {func(Ref) Decl {
			return Decl{Kind: "job", Name: "j", Class: Task, Owns: Rel("o"), Spec: spec{In: foreign}}
		}, "not declared before it"},
		"colliding ref": {func(Ref) Decl {
			return Decl{Kind: "job", Name: "j", Class: Task, Owns: Rel("o"), Spec: spec{In: collide}}
		}, `names path "secret.txt", but src.a owns "a"`},
		"ref inside owner": {func(Ref) Decl {
			return Decl{Kind: "job", Name: "j", Class: Task, Owns: Rel("o"), Spec: spec{In: within}}
		}, `names path "a/inner.txt", but src.a owns "a"`},
		"escaping join": {func(a Ref) Decl {
			return Decl{Kind: "job", Name: "j", Class: Task, Owns: Rel("o"), Spec: spec{Out: a.Join("../z")}}
		}, "names path"},
		"same path":        {func(Ref) Decl { return Decl{Kind: "src", Name: "n", Class: Resource, Owns: Rel("a"), Spec: 1} }, "already owned by src.a"},
		"same path folded": {func(Ref) Decl { return Decl{Kind: "src", Name: "n", Class: Resource, Owns: Rel("A"), Spec: 1} }, "already owned by src.a"},
		"nested resource":  {func(a Ref) Decl { return Decl{Kind: "src", Name: "n", Class: Resource, Owns: a.Join("n"), Spec: 1} }, "only a task that references"},
		"nested folded":    {func(Ref) Decl { return Decl{Kind: "src", Name: "n", Class: Resource, Owns: Rel("A/n"), Spec: 1} }, "only a task that references"},
		"unreferenced":     {func(Ref) Decl { return Decl{Kind: "job", Name: "j", Class: Task, Owns: Rel("a/n"), Spec: 1} }, "only a task that references"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			g := NewGraph("t")
			a := g.Declare(Decl{Kind: "src", Name: "a", Class: Resource, Owns: Rel("a"), Spec: 1})
			g.Declare(c.d(a))
			_, err := g.Intent()
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestATaskMayWriteInsideAResourceItReferences(t *testing.T) {
	g := NewGraph("t")
	dir := g.Declare(Decl{Kind: "dir", Name: "d", Class: Resource, Owns: Rel("d"), Spec: 1})
	g.Declare(Decl{Kind: "job", Name: "j", Class: Task, Owns: dir.Join("out"), Spec: spec{Out: dir.Join("out")}})
	if _, err := g.Intent(); err != nil {
		t.Fatal(err)
	}
}

// A saved plan's intent never went through Declare; Validate applies the
// same rules to it, including to a Ref forged through JSON.
func TestValidateRejectsAnEditedIntent(t *testing.T) {
	g := NewGraph("t")
	a := g.Declare(Decl{Kind: "src", Name: "a", Class: Resource, Owns: Rel("a"), Spec: 1})
	g.Declare(Decl{Kind: "job", Name: "j", Class: Task, Owns: Rel("o"), Spec: spec{In: a}})
	in, err := g.Intent()
	if err != nil {
		t.Fatal(err)
	}
	if err := in.Validate(); err != nil {
		t.Fatalf("valid intent rejected: %v", err)
	}
	edits := map[string]func(in *Intent){
		"forged ref path": func(in *Intent) {
			in.Nodes[1].Spec = json.RawMessage(`{"in":{"$ref":"src.a","path":"secret.txt"},"out":{"path":""}}`)
		},
		"forged ref inside": func(in *Intent) {
			in.Nodes[1].Spec = json.RawMessage(`{"in":{"$ref":"src.a","path":"a/inner.txt"},"out":{"path":""}}`)
		},
		"ref not a dep":   func(in *Intent) { in.Nodes[1].Deps = nil },
		"reordered":       func(in *Intent) { in.Nodes[0], in.Nodes[1] = in.Nodes[1], in.Nodes[0] },
		"evidence folded": func(in *Intent) { in.Nodes[0].Owns = ".Wb" },
	}
	for name, edit := range edits {
		t.Run(name, func(t *testing.T) {
			bad := Intent{Workflow: in.Workflow, Nodes: slices.Clone(in.Nodes)}
			edit(&bad)
			if err := bad.Validate(); err == nil {
				t.Fatal("edited intent validated")
			}
		})
	}
}

func TestNewPathCannotContainAnExistingOne(t *testing.T) {
	g := NewGraph("t")
	g.Declare(Decl{Kind: "src", Name: "inner", Class: Resource, Owns: Rel("d/inner.txt"), Spec: 1})
	g.Declare(Decl{Kind: "dir", Name: "outer", Class: Resource, Owns: Rel("d"), Spec: 1})
	if _, err := g.Intent(); err == nil || !strings.Contains(err.Error(), "would contain") {
		t.Fatalf("err = %v, want containment refusal", err)
	}
}

func TestEvaluateRecordsParamsAndRejectsUnknownOnes(t *testing.T) {
	define := func(p *Params) *Graph {
		g := NewGraph("t")
		g.Declare(Decl{Kind: "src", Name: "a", Class: Resource, Owns: Rel("a"), Spec: p.String("text", "default")})
		return g
	}
	in, err := Evaluate(define, nil)
	if err != nil || in.Params["text"] != "default" {
		t.Fatalf("Evaluate = %+v, %v", in.Params, err)
	}
	if _, err := Evaluate(define, map[string]string{"txt": "typo"}); err == nil || !strings.Contains(err.Error(), "unknown param(s) txt") {
		t.Fatalf("err = %v, want unknown param error", err)
	}
}

func TestEvaluateCatchesANondeterministicDefinition(t *testing.T) {
	calls := 0
	define := func(*Params) *Graph {
		calls++ // stands in for time.Now, rand, or reading a changing file
		g := NewGraph("t")
		g.Declare(Decl{Kind: "src", Name: "a", Class: Resource, Owns: Rel("a"), Spec: calls})
		return g
	}
	if _, err := Evaluate(define, nil); err == nil || !strings.Contains(err.Error(), "not deterministic") {
		t.Fatalf("err = %v, want nondeterminism error", err)
	}
}

func TestIntentDigestIsStable(t *testing.T) {
	build := func() Intent {
		g := NewGraph("t")
		a := g.Declare(Decl{Kind: "src", Name: "a", Class: Resource, Owns: Rel("a"), Spec: map[string]int{"z": 1, "y": 2}})
		g.Declare(Decl{Kind: "job", Name: "j", Class: Task, Owns: Rel("o"), Spec: spec{In: a, Out: Rel("o")}})
		in, err := g.Intent()
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	first, second := build(), build()
	if first.Digest() != second.Digest() {
		t.Error("identical declarations produced different digests")
	}
}
