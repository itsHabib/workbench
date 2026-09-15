// Package wb is the typed authoring API. A workflow is an ordinary Go
// function that declares nodes on a Graph through adapter constructors
// and returns it. Declaring performs no effects. The Graph normalizes to
// an Intent, plain JSON-shaped data, and the Intent is the only thing the
// engine consumes: nothing after evaluation runs workflow code.
package wb

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// Class separates persistent desired state from one-shot work.
type Class string

const (
	// Resource is persistent desired state: every apply converges it.
	Resource Class = "resource"
	// Task is one-shot work: it runs again only when its result is stale.
	Task Class = "task"
)

// Address identifies a node as "<kind>.<name>". It is the node's
// identity: renaming a node makes it a different node with no history.
type Address string

// Node is one normalized declaration.
type Node struct {
	Address Address         `json:"address"`
	Kind    string          `json:"kind"`
	Class   Class           `json:"class"`
	Owns    string          `json:"owns"`
	Deps    []Address       `json:"deps,omitempty"`
	Spec    json.RawMessage `json:"spec"`
}

// Intent is the normalized form of a workflow evaluation.
type Intent struct {
	Workflow string            `json:"workflow"`
	Params   map[string]string `json:"params,omitempty"`
	Nodes    []Node            `json:"nodes"` // dependency order: declaration order
}

// Digest identifies the intent's content.
func (in Intent) Digest() string {
	b, err := json.Marshal(in)
	if err != nil {
		// Every field is a string, slice or already-validated raw JSON.
		panic(fmt.Sprintf("wb: marshal intent: %v", err))
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Node looks a node up by address.
func (in Intent) Node(a Address) (Node, bool) {
	for _, n := range in.Nodes {
		if n.Address == a {
			return n, true
		}
	}
	return Node{}, false
}

// Decl is what an adapter's typed constructor passes to Declare.
type Decl struct {
	Kind  string
	Name  string
	Class Class
	Owns  Path // the one workspace path this node owns
	Spec  any  // plain data; every Ref or anchored Path inside it is a dependency
}

// Graph collects declarations. Errors are sticky: Declare records them and
// Intent reports all of them, so workflow code reads as a list of
// declarations with no error plumbing.
type Graph struct {
	name string
	set  nodeSet
	errs []error
}

// NewGraph starts a workflow graph.
func NewGraph(name string) *Graph {
	return &Graph{name: name, set: newNodeSet()}
}

// Declare adds a node and returns a Ref to it. Adapter constructors call
// it; workflows call the constructors.
func (g *Graph) Declare(d Decl) Ref {
	addr := Address(d.Kind + "." + d.Name)
	n, err := normalize(addr, d)
	if err == nil {
		err = g.set.add(n)
	}
	if err != nil {
		g.errs = append(g.errs, fmt.Errorf("%s: %w", addr, err))
	}
	return Ref{addr: addr, path: d.Owns.rel}
}

// Intent returns the normalized intent, or every declaration error.
func (g *Graph) Intent() (Intent, error) {
	if len(g.errs) > 0 {
		return Intent{}, errors.Join(g.errs...)
	}
	return Intent{Workflow: g.name, Nodes: g.set.list()}, nil
}

// Validate re-checks an intent with the same rules Declare applies: names,
// paths, ownership, and that every reference names an earlier node and a
// path that node owns. The engine validates every intent it consumes,
// including one read back from a saved plan, which never went through
// Declare.
func (in Intent) Validate() error {
	set := newNodeSet()
	var errs []error
	for _, n := range in.Nodes {
		err := checkIdentity(n)
		if err == nil {
			err = set.add(n)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", n.Address, err))
		}
	}
	return errors.Join(errs...)
}

// normalize turns a declaration into a node. Dependencies are derived
// from the spec itself, so the intent and its dependency list can never
// disagree.
func normalize(addr Address, d Decl) (Node, error) {
	n := Node{Address: addr, Kind: d.Kind, Class: d.Class, Owns: d.Owns.rel}
	if err := checkIdentity(n); err != nil {
		return Node{}, err
	}
	spec, err := json.Marshal(d.Spec)
	if err != nil {
		return Node{}, fmt.Errorf("spec must be plain data: %w", err)
	}
	uses, err := refsIn(spec)
	if err != nil {
		return Node{}, err
	}
	seen := map[Address]bool{}
	for _, u := range uses {
		seen[u.addr] = true
	}
	if d.Owns.from != "" {
		seen[d.Owns.from] = true
	}
	for a := range seen {
		n.Deps = append(n.Deps, a)
	}
	slices.Sort(n.Deps)
	n.Spec = spec
	return n, nil
}
