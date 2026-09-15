// Package file is the text-file resource: a workspace file whose whole
// content is declared by the workflow. The node owns its path. Content
// changed outside the workflow is drift: the next plan shows it as a diff
// and the apply after that overwrites it.
package file

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/engine"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/wb"
)

// Kind is the node kind this package declares and adapts.
const Kind = "file"

// maxText bounds the content kept in plans for readable diffs.
const maxText = 4096

// Spec is a file's desired state.
type Spec struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// New declares a file resource. The returned Ref is how other nodes read it.
func New(g *wb.Graph, name string, s Spec) wb.Ref {
	return g.Declare(wb.Decl{Kind: Kind, Name: name, Class: wb.Resource, Owns: wb.Rel(s.Path), Spec: s})
}

// Adapter plans and applies file nodes.
type Adapter struct{}

// Kind implements engine.Adapter.
func (Adapter) Kind() string { return Kind }

// Plan compares the file on disk with the declared content.
func (Adapter) Plan(ws *foundation.Workspace, n wb.Node) (engine.Change, error) {
	s, err := decode(n)
	if err != nil {
		return engine.Change{}, err
	}
	cur, err := ws.ReadFile(s.Path)
	if err != nil {
		return engine.Change{}, err
	}
	ch := engine.Change{Op: engine.None, Before: state(cur.Exists, cur.Content), After: state(true, []byte(s.Content))}
	if !cur.Exists {
		ch.Op = engine.Create
		return ch, nil
	}
	if ch.Before.Digest != ch.After.Digest {
		ch.Op = engine.Update
	}
	return ch, nil
}

// Apply writes the declared content atomically.
func (Adapter) Apply(ws *foundation.Workspace, n wb.Node, _ engine.Change) error {
	s, err := decode(n)
	if err != nil {
		return err
	}
	return ws.WriteFile(s.Path, []byte(s.Content))
}

func decode(n wb.Node) (Spec, error) {
	var s Spec
	if err := json.Unmarshal(n.Spec, &s); err != nil {
		return Spec{}, fmt.Errorf("decode file spec: %w", err)
	}
	if s.Path != n.Owns {
		return Spec{}, fmt.Errorf("spec path %q is not the owned path %q", s.Path, n.Owns)
	}
	return s, nil
}

// state keeps small UTF-8 content as text so plans can show diffs.
func state(exists bool, b []byte) engine.State {
	if !exists {
		return engine.State{}
	}
	st := engine.State{Exists: true, Digest: foundation.Digest(b)}
	if len(b) <= maxText && utf8.Valid(b) {
		st.Text = string(b)
	}
	return st
}
