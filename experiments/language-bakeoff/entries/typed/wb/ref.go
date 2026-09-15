package wb

import (
	"encoding/json"
	"path"
)

// Ref points at a declared node and the path it owns. In Go code only
// Graph.Declare hands out Refs, so a workflow cannot spell a reference to
// an undeclared node, and because a node's Ref exists only after the node
// is declared, it cannot write a cycle either. Placing a Ref in another
// node's spec is how a dependency is written. Refs also decode from JSON
// (adapters read specs back), so the rules are enforced on data too:
// every reference must name an earlier node of the same graph and the
// path that node owns (see Intent.Validate).
type Ref struct {
	addr Address
	path string
}

// Address is the referenced node's identity.
func (r Ref) Address() Address { return r.addr }

// Path is the path the node owns, still carrying the reference.
func (r Ref) Path() Path { return Path{rel: r.path, from: r.addr} }

// Join is a path inside the node's path that depends on the node.
func (r Ref) Join(elem string) Path { return Path{rel: path.Join(r.path, elem), from: r.addr} }

// String is the owned path.
func (r Ref) String() string { return r.path }

// Path is a slash-separated workspace-relative path, optionally anchored
// to the node it lives in. An anchored path declares a dependency on that
// node wherever it is used.
type Path struct {
	rel  string
	from Address
}

// Rel is a literal workspace path with no dependency.
func Rel(p string) Path { return Path{rel: p} }

// String is the path.
func (p Path) String() string { return p.rel }

// From is the node the path is anchored to, or "".
func (p Path) From() Address { return p.from }

// refJSON is how a Ref appears in the intent: "$ref" names the node and
// path must be exactly the path it owns.
type refJSON struct {
	Ref  Address `json:"$ref,omitempty"`
	Path string  `json:"path"`
}

// pathJSON is how a Path appears: "$in" names the node whose path it lies
// inside (or is). Distinct keys let validation hold a Ref to the exact
// owned path while allowing a joined Path to point inside it.
type pathJSON struct {
	In   Address `json:"$in,omitempty"`
	Path string  `json:"path"`
}

// MarshalJSON renders the reference and its path.
func (r Ref) MarshalJSON() ([]byte, error) { return json.Marshal(refJSON{Ref: r.addr, Path: r.path}) }

// UnmarshalJSON reads what MarshalJSON wrote.
func (r *Ref) UnmarshalJSON(b []byte) error {
	var v refJSON
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	r.addr, r.path = v.Ref, v.Path
	return nil
}

// MarshalJSON renders the path and, when anchored, its node.
func (p Path) MarshalJSON() ([]byte, error) { return json.Marshal(pathJSON{In: p.from, Path: p.rel}) }

// UnmarshalJSON reads what MarshalJSON wrote.
func (p *Path) UnmarshalJSON(b []byte) error {
	var v pathJSON
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	p.from, p.rel = v.In, v.Path
	return nil
}
