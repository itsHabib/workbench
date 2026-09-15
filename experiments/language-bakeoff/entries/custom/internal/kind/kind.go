// Package kind is the extension contract. An adapter describes its
// attributes with a Schema and implements either Resource (a kept resource
// wb converges) or Work (one-shot work wb replays when stale). The parser
// and planner only ever see these interfaces; adding a kind means writing an
// adapter and registering it, never editing them.
package kind

import (
	"context"
	"fmt"
	"sort"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/intent"
)

// Lifecycle is how wb treats a declaration over time.
type Lifecycle string

// The two lifecycles a declaration can have.
const (
	Keep Lifecycle = "keep" // persistent desired state, converged on every apply
	Run  Lifecycle = "run"  // one-shot work, replayed only when its inputs change
)

// FieldType says how the checker validates an attribute and what the
// planner may infer from it.
type FieldType int

// Field types.
const (
	Text      FieldType = iota // one string; literal or reference
	List                       // one or more literal strings
	Path                       // one workspace path read or used; literal or reference; tracked as an input
	OwnedPath                  // one workspace path the declaration owns; literal only
)

func (t FieldType) String() string {
	return [...]string{"text", "list", "path", "owned path"}[t]
}

// Field is one attribute an adapter accepts.
type Field struct {
	Name     string
	Type     FieldType
	Required bool
}

// Schema describes an adapter kind to the checker.
type Schema struct {
	Kind    string
	Fields  []Field
	Exports []string // attributes other declarations may reference
}

// Field looks up an attribute by name.
func (s Schema) Field(name string) (Field, bool) {
	for _, f := range s.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// Exported reports whether other declarations may reference name.
func (s Schema) Exported(name string) bool {
	for _, e := range s.Exports {
		if e == name {
			return true
		}
	}
	return false
}

// Fact is one backend observation. The planner records it in the plan and
// apply compares it verbatim, so a changed fact makes the plan stale.
type Fact struct {
	Subject string // what was observed, e.g. "notes.txt"
	Digest  string // foundation.Absent, foundation.Dir, "sha256:..."
	Summary string // for people: "44 bytes"
}

// Action is a resource adapter's decision for one declaration.
type Action string

// Resource actions.
const (
	OK       Action = "ok"
	Create   Action = "create"
	Update   Action = "update"
	Conflict Action = "conflict" // wb will not converge this safely; apply refuses
)

// Proposal is what a resource adapter would do, and what dependents will
// observe afterwards.
type Proposal struct {
	Action  Action
	Summary string   // "create notes.txt (44 bytes)"
	Detail  []string // optional, such as changed lines
	Version string   // what dependents observe once the resource is converged
}

// Resource converges persistent desired state.
type Resource interface {
	Schema() Schema
	Observe(ws *foundation.Workspace, d intent.Decl) (Fact, error)
	Propose(ws *foundation.Workspace, d intent.Decl, now Fact) (Proposal, error)
	Apply(ws *foundation.Workspace, d intent.Decl) (Fact, error)
}

// Work performs one-shot effects. The planner, not the adapter, decides
// from retained evidence whether the work is stale.
type Work interface {
	Schema() Schema
	Observe(ws *foundation.Workspace, d intent.Decl) (Fact, error) // its owned output as it is now
	Run(ctx context.Context, ws *foundation.Workspace, d intent.Decl) (Fact, error)
}

// Registry maps kind names to adapters.
type Registry struct {
	keep map[string]Resource
	run  map[string]Work
}

// NewRegistry registers resource and work adapters. A kind may be
// registered once.
func NewRegistry(resources []Resource, works []Work) (*Registry, error) {
	r := &Registry{keep: map[string]Resource{}, run: map[string]Work{}}
	for _, a := range resources {
		if err := r.claim(a.Schema().Kind); err != nil {
			return nil, err
		}
		r.keep[a.Schema().Kind] = a
	}
	for _, a := range works {
		if err := r.claim(a.Schema().Kind); err != nil {
			return nil, err
		}
		r.run[a.Schema().Kind] = a
	}
	return r, nil
}

func (r *Registry) claim(name string) error {
	_, keep := r.keep[name]
	_, run := r.run[name]
	if name == "" || keep || run {
		return fmt.Errorf("kind %q registered twice or unnamed", name)
	}
	return nil
}

// Lookup returns a kind's schema and lifecycle.
func (r *Registry) Lookup(name string) (Schema, Lifecycle, bool) {
	if a, ok := r.keep[name]; ok {
		return a.Schema(), Keep, true
	}
	if a, ok := r.run[name]; ok {
		return a.Schema(), Run, true
	}
	return Schema{}, "", false
}

// Kinds lists registered kind names in order.
func (r *Registry) Kinds() []string {
	var names []string
	for name := range r.keep {
		names = append(names, name)
	}
	for name := range r.run {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Resource returns the adapter for a kept kind.
func (r *Registry) Resource(name string) (Resource, bool) {
	a, ok := r.keep[name]
	return a, ok
}

// Work returns the adapter for a one-shot kind.
func (r *Registry) Work(name string) (Work, bool) {
	a, ok := r.run[name]
	return a, ok
}

// ObservePath describes a workspace path as a fact. It is the observation
// most file-owning adapters need.
func ObservePath(ws *foundation.Workspace, path string) (Fact, error) {
	digest, size, err := ws.Observe(path)
	if err != nil {
		return Fact{}, err
	}
	summary := fmt.Sprintf("%d bytes", size)
	if digest == foundation.Absent || digest == foundation.Dir || digest == foundation.Other {
		summary = digest
	}
	return Fact{Subject: path, Digest: digest, Summary: summary}, nil
}
