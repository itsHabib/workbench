package dag

import (
	"fmt"
	"sort"

	"schedlab/errs"
)

type Graph struct {
	preds map[string]map[string]bool
	succs map[string]map[string]bool
}

func New() *Graph {
	return &Graph{preds: map[string]map[string]bool{}, succs: map[string]map[string]bool{}}
}

func (g *Graph) AddNode(id string) error {
	if id == "" {
		return fmt.Errorf("dag: empty node: %w", errs.ErrInvalid)
	}
	if _, ok := g.preds[id]; ok {
		return fmt.Errorf("dag: node %q: %w", id, errs.ErrConflict)
	}
	g.preds[id] = map[string]bool{}
	g.succs[id] = map[string]bool{}
	return nil
}

func (g *Graph) reaches(from, to string) bool {
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n == to {
			return true
		}
		for s := range g.succs[n] {
			if !seen[s] {
				seen[s] = true
				stack = append(stack, s)
			}
		}
	}
	return false
}

func (g *Graph) AddEdge(from, to string) error {
	for _, id := range []string{from, to} {
		if _, ok := g.preds[id]; !ok {
			return fmt.Errorf("dag: node %q: %w", id, errs.ErrNotFound)
		}
	}
	if from == to {
		return fmt.Errorf("dag: self edge %q: %w", from, errs.ErrInvalid)
	}
	if g.succs[from][to] {
		return fmt.Errorf("dag: edge %q->%q: %w", from, to, errs.ErrConflict)
	}
	if g.reaches(to, from) {
		return fmt.Errorf("dag: edge %q->%q: %w", from, to, errs.ErrCycle)
	}
	g.succs[from][to] = true
	g.preds[to][from] = true
	return nil
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (g *Graph) Nodes() []string { return sorted(keys(g.preds)) }

func keys(m map[string]map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func (g *Graph) Len() int { return len(g.preds) }

func (g *Graph) Deps(id string) ([]string, error) {
	m, ok := g.preds[id]
	if !ok {
		return nil, fmt.Errorf("dag: node %q: %w", id, errs.ErrNotFound)
	}
	return sorted(m), nil
}

func (g *Graph) Dependents(id string) ([]string, error) {
	m, ok := g.succs[id]
	if !ok {
		return nil, fmt.Errorf("dag: node %q: %w", id, errs.ErrNotFound)
	}
	return sorted(m), nil
}

func (g *Graph) Order() []string {
	done := map[string]bool{}
	out := make([]string, 0, len(g.preds))
	for len(out) < len(g.preds) {
		ready := g.Ready(done)
		out = append(out, ready[0])
		done[ready[0]] = true
	}
	return out
}

func (g *Graph) Ready(done map[string]bool) []string {
	var out []string
	for id, preds := range g.preds {
		if done[id] {
			continue
		}
		ok := true
		for p := range preds {
			if !done[p] {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
