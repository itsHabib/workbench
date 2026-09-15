// Package engine plans and applies an intent through adapters. It is
// generic over node kinds and never imports a concrete adapter, so adding
// a kind never edits this package. It holds no state of its own between
// runs: every plan is computed from the workspace's observed facts and
// the foundation's evidence journal.
package engine

import (
	"context"
	"fmt"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/wb"
)

// Op is a planned action.
type Op string

// Planned actions. Replace is destructive: delete, then recreate.
const (
	None    Op = "none"
	Create  Op = "create"
	Update  Op = "update"
	Replace Op = "replace"
	Run     Op = "run"
)

// State is an adapter's observation of the object a node owns. An
// adapter's observed and desired digests must be comparable.
type State struct {
	Exists bool   `json:"exists"`
	Digest string `json:"digest,omitempty"`
	Text   string `json:"text,omitempty"` // short readable rendering, for diffs only
}

// Change is a resource adapter's proposal for one node.
type Change struct {
	Op     Op
	Before State // observed now
	After  State // desired
	Note   string
}

// Adapter handles one node kind. It must also implement Resource or
// Task, matching the class of the nodes it declares.
type Adapter interface {
	Kind() string
}

// Resource converges persistent desired state.
type Resource interface {
	Adapter
	// Plan observes the node's object and proposes a change. No effects.
	Plan(ws *foundation.Workspace, n wb.Node) (Change, error)
	// Apply performs a change the engine has just re-verified.
	Apply(ws *foundation.Workspace, n wb.Node, ch Change) error
}

// Task is one-shot work that produces the output artifact it owns.
type Task interface {
	Adapter
	// Observe reports the output artifact. No effects.
	Observe(ws *foundation.Workspace, n wb.Node) (State, error)
	// Run does the work once and reports the output it produced.
	Run(ctx context.Context, ws *foundation.Workspace, n wb.Node) (State, error)
}

// Registry maps node kinds to adapters. The workflow program builds it
// explicitly; the engine only looks things up.
type Registry struct {
	byKind map[string]Adapter
}

// NewRegistry registers adapters by kind.
func NewRegistry(adapters ...Adapter) (Registry, error) {
	r := Registry{byKind: map[string]Adapter{}}
	for _, a := range adapters {
		if _, dup := r.byKind[a.Kind()]; dup {
			return Registry{}, fmt.Errorf("two adapters for kind %q", a.Kind())
		}
		r.byKind[a.Kind()] = a
	}
	return r, nil
}

func (r Registry) resource(n wb.Node) (Resource, error) {
	a, ok := r.byKind[n.Kind]
	if !ok {
		return nil, fmt.Errorf("no adapter registered for kind %q", n.Kind)
	}
	res, ok := a.(Resource)
	if !ok || n.Class != wb.Resource {
		return nil, fmt.Errorf("adapter for %q does not serve %s nodes", n.Kind, n.Class)
	}
	return res, nil
}

func (r Registry) task(n wb.Node) (Task, error) {
	a, ok := r.byKind[n.Kind]
	if !ok {
		return nil, fmt.Errorf("no adapter registered for kind %q", n.Kind)
	}
	t, ok := a.(Task)
	if !ok || n.Class != wb.Task {
		return nil, fmt.Errorf("adapter for %q does not serve %s nodes", n.Kind, n.Class)
	}
	return t, nil
}

// check reports whether an adapter serves n.
func (r Registry) check(n wb.Node) error {
	if n.Class == wb.Task {
		_, err := r.task(n)
		return err
	}
	_, err := r.resource(n)
	return err
}

// observe reports a node's current object, whatever its class.
func (r Registry) observe(ws *foundation.Workspace, n wb.Node) (State, error) {
	if n.Class == wb.Task {
		t, err := r.task(n)
		if err != nil {
			return State{}, err
		}
		return t.Observe(ws, n)
	}
	res, err := r.resource(n)
	if err != nil {
		return State{}, err
	}
	ch, err := res.Plan(ws, n)
	return ch.Before, err
}

func same(a, b State) bool { return a.Exists == b.Exists && a.Digest == b.Digest }
