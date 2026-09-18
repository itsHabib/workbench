package dag

import (
	"errors"
	"reflect"
	"testing"

	"schedlab/errs"
)

func build(t *testing.T, nodes []string, edges [][2]string) *Graph {
	t.Helper()
	g := New()
	for _, n := range nodes {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			t.Fatalf("%v: %v", e, err)
		}
	}
	return g
}

func TestHiddenNodes(t *testing.T) {
	g := New()
	if err := g.AddNode(""); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("empty: %v", err)
	}
	for _, n := range []string{"b", "a", "c"} {
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddNode("a"); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("dup: %v", err)
	}
	if got := g.Nodes(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) || g.Len() != 3 {
		t.Fatalf("%v %d", got, g.Len())
	}
	deps, err := g.Deps("a")
	if err != nil || deps == nil || len(deps) != 0 {
		t.Fatalf("deps of a lone node: %v %v", deps, err)
	}
	if _, err := g.Deps("zz"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("%v", err)
	}
	if _, err := g.Dependents("zz"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("%v", err)
	}
}

func TestHiddenEdges(t *testing.T) {
	g := build(t, []string{"a", "b", "c"}, [][2]string{{"a", "b"}, {"a", "c"}, {"b", "c"}})
	if err := g.AddEdge("a", "b"); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("dup edge: %v", err)
	}
	if err := g.AddEdge("a", "a"); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("self: %v", err)
	}
	if err := g.AddEdge("a", "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("missing to: %v", err)
	}
	if err := g.AddEdge("nope", "a"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("missing from: %v", err)
	}
	if err := g.AddEdge("nope", "nope"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("missing beats self: %v", err)
	}
	deps, _ := g.Deps("c")
	dependents, _ := g.Dependents("a")
	if !reflect.DeepEqual(deps, []string{"a", "b"}) || !reflect.DeepEqual(dependents, []string{"b", "c"}) {
		t.Fatalf("%v %v", deps, dependents)
	}
	if d, _ := g.Dependents("c"); d == nil || len(d) != 0 {
		t.Fatalf("sink dependents %v", d)
	}
}

func TestHiddenCycleRefused(t *testing.T) {
	g := build(t, []string{"a", "b", "c", "d"}, [][2]string{{"a", "b"}, {"b", "c"}, {"c", "d"}})
	for _, e := range [][2]string{{"d", "a"}, {"c", "a"}, {"b", "a"}, {"d", "b"}} {
		if err := g.AddEdge(e[0], e[1]); !errors.Is(err, errs.ErrCycle) {
			t.Fatalf("%v: %v", e, err)
		}
	}
	deps, _ := g.Deps("a")
	if len(deps) != 0 {
		t.Fatalf("a refused edge changed the graph: %v", deps)
	}
	if err := g.AddEdge("a", "d"); err != nil {
		t.Fatalf("forward edge: %v", err)
	}
	if !reflect.DeepEqual(g.Order(), []string{"a", "b", "c", "d"}) {
		t.Fatalf("%v", g.Order())
	}
}

func TestHiddenOrderIsSmallestFirst(t *testing.T) {
	g := build(t, []string{"z", "y", "x", "w"}, [][2]string{{"z", "x"}, {"y", "w"}, {"x", "w"}})
	if got := g.Order(); !reflect.DeepEqual(got, []string{"y", "z", "x", "w"}) {
		t.Fatalf("%v", got)
	}
	g2 := build(t, []string{"b", "a", "c"}, nil)
	if got := g2.Order(); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("%v", got)
	}
	if got := New().Order(); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestHiddenReady(t *testing.T) {
	g := build(t, []string{"fetch", "parse", "index", "report"}, [][2]string{{"fetch", "parse"}, {"parse", "index"}, {"parse", "report"}, {"fetch", "report"}})
	if got := g.Ready(nil); !reflect.DeepEqual(got, []string{"fetch"}) {
		t.Fatalf("%v", got)
	}
	if got := g.Ready(map[string]bool{"fetch": true}); !reflect.DeepEqual(got, []string{"parse"}) {
		t.Fatalf("%v", got)
	}
	if got := g.Ready(map[string]bool{"fetch": true, "parse": true}); !reflect.DeepEqual(got, []string{"index", "report"}) {
		t.Fatalf("%v", got)
	}
	if got := g.Ready(map[string]bool{"fetch": true, "parse": false, "index": true}); !reflect.DeepEqual(got, []string{"parse"}) {
		t.Fatalf("false counts as not done: %v", got)
	}
	if got := g.Ready(map[string]bool{"fetch": true, "parse": true, "index": true, "report": true}); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestHiddenDiamondDoesNotCycle(t *testing.T) {
	g := build(t, []string{"a", "b", "c", "d"}, [][2]string{{"a", "b"}, {"a", "c"}, {"b", "d"}})
	if err := g.AddEdge("c", "d"); err != nil {
		t.Fatalf("diamond: %v", err)
	}
	if err := g.AddEdge("d", "a"); !errors.Is(err, errs.ErrCycle) {
		t.Fatalf("closing the diamond: %v", err)
	}
	deps, _ := g.Deps("d")
	if !reflect.DeepEqual(deps, []string{"b", "c"}) || !reflect.DeepEqual(g.Order(), []string{"a", "b", "c", "d"}) {
		t.Fatalf("%v %v", deps, g.Order())
	}
}
