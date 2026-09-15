// Package intent is the normalized representation between the language and
// the planner: every reference resolved to a value, every declaration in
// dependency order. It is plain data. The planner never sees syntax, so a
// different front end could produce the same intent.
package intent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// Intent is what a source file asks for.
type Intent struct {
	Source string `json:"source"`
	Decls  []Decl `json:"decls"` // dependency order; ties keep source order
}

// Decl is one declaration with its references resolved.
type Decl struct {
	ID        string              `json:"id"`
	Lifecycle string              `json:"lifecycle"` // keep or run
	Kind      string              `json:"kind"`
	Line      int                 `json:"line"`
	Attrs     map[string][]string `json:"attrs"`
	Refs      map[string]string   `json:"refs,omitempty"`  // attribute -> "decl.attr" it was resolved from
	Owns      []string            `json:"owns,omitempty"`  // workspace paths this declaration owns
	Needs     []string            `json:"needs,omitempty"` // declarations that must be settled first
}

// One returns a single-valued attribute, or "" when it is unset.
func (d Decl) One(name string) string {
	v := d.Attrs[name]
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// All returns a list attribute.
func (d Decl) All(name string) []string { return d.Attrs[name] }

// Digest fingerprints what the declaration asks for, independent of its
// name and source position. Work whose digest changes is stale.
func (d Decl) Digest() string {
	b, err := json.Marshal(struct {
		Kind  string              `json:"kind"`
		Attrs map[string][]string `json:"attrs"`
	}{d.Kind, d.Attrs})
	if err != nil {
		panic(err) // a map of string slices always marshals
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
