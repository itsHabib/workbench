// Package scratchdir is the third adapter: a disposable directory
// resource. Its contents belong to the workflow and may be deleted at any
// time; changing the declared generation wipes and recreates it, which
// the plan shows as a destructive replace. It deletes only a directory
// that carries its ownership marker, so it never adopts, and then
// destroys, a non-empty directory it did not create. It also owns two
// working names beside its path (".<name>.wb-new" and ".<name>.wb-old").
package scratchdir

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/engine"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/wb"
)

// Kind is the node kind this package declares and adapts.
const Kind = "scratchdir"

// marker sits inside the directory and holds its generation.
const marker = ".wb-scratchdir"

// Spec is a scratch directory's desired state.
type Spec struct {
	Path       string `json:"path"`
	Generation string `json:"generation"` // change it to wipe the directory
}

// New declares a scratch directory. Put a task's output inside it with
// the returned Ref's Join, which also makes the task depend on it.
func New(g *wb.Graph, name string, s Spec) wb.Ref {
	return g.Declare(wb.Decl{Kind: Kind, Name: name, Class: wb.Resource, Owns: wb.Rel(s.Path), Spec: s})
}

// Adapter plans and applies scratch directories.
type Adapter struct{}

// Kind implements engine.Adapter.
func (Adapter) Kind() string { return Kind }

// Plan compares the directory's marker with the declared generation.
func (Adapter) Plan(ws *foundation.Workspace, n wb.Node) (engine.Change, error) {
	s, err := decode(n)
	if err != nil {
		return engine.Change{}, err
	}
	want := state(s.Generation)
	dir, err := ws.StatDir(s.Path)
	if err != nil || !dir.Exists {
		return engine.Change{Op: engine.Create, After: want}, err
	}
	if !dir.IsDir {
		return engine.Change{}, fmt.Errorf("%s exists and is not a directory; refusing to manage it", s.Path)
	}
	m, err := ws.ReadFile(path.Join(s.Path, marker))
	if err != nil {
		return engine.Change{}, err
	}
	if !m.Exists {
		return adopt(ws, s, want)
	}
	have := state(string(m.Content))
	if have.Digest == want.Digest {
		return engine.Change{Op: engine.None, Before: have, After: want}, nil
	}
	return engine.Change{Op: engine.Replace, Before: have, After: want,
		Note: "deletes everything inside " + s.Path + "/"}, nil
}

// adopt takes over an unmarked directory only if it is empty, such as one
// made by hand with mkdir: it holds nothing to lose.
func adopt(ws *foundation.Workspace, s Spec, want engine.State) (engine.Change, error) {
	empty, err := ws.DirEmpty(s.Path)
	if err != nil {
		return engine.Change{}, err
	}
	if !empty {
		return engine.Change{}, fmt.Errorf("%s exists without the %s ownership marker and is not empty; "+
			"refusing to adopt or delete it (move it away or choose another path)", s.Path, marker)
	}
	return engine.Change{Op: engine.Create, After: want, Note: "adopts the existing empty directory"}, nil
}

// Apply builds the new, marked directory beside the old one and swaps it
// in with renames. A crash at any point leaves the old directory, the new
// one, or nothing at the path (never an unmarked directory), plus working
// copies that the next apply of this node removes first. The slow part,
// deleting the old contents, happens last, after the new directory is in
// place.
func (Adapter) Apply(ws *foundation.Workspace, n wb.Node, ch engine.Change) error {
	s, err := decode(n)
	if err != nil {
		return err
	}
	fresh, old := sibling(s.Path, "new"), sibling(s.Path, "old")
	for _, leftover := range []string{fresh, old} {
		if err := ws.RemoveAll(leftover); err != nil {
			return err
		}
	}
	if err := ws.MakeDir(fresh); err != nil {
		return err
	}
	if err := ws.WriteFile(path.Join(fresh, marker), []byte(s.Generation)); err != nil {
		return err
	}
	if err := vacate(ws, s.Path, old, ch.Op); err != nil {
		return err
	}
	if err := ws.Rename(fresh, s.Path); err != nil {
		return err
	}
	return ws.RemoveAll(old)
}

// vacate empties the path for the new directory. A replace moves the owned
// directory aside; a create removes an adopted empty directory, which
// fails, deleting nothing, if it gained entries since the plan.
func vacate(ws *foundation.Workspace, p, old string, op engine.Op) error {
	if op == engine.Replace {
		return ws.Rename(p, old)
	}
	err := ws.Remove(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// sibling is a working name next to p, such as ".work.wb-new". Declared
// paths may not contain ".wb-", so no node can own these names.
func sibling(p, role string) string {
	dir, base := path.Split(p)
	return dir + "." + base + ".wb-" + role
}

func decode(n wb.Node) (Spec, error) {
	var s Spec
	if err := json.Unmarshal(n.Spec, &s); err != nil {
		return Spec{}, fmt.Errorf("decode scratchdir spec: %w", err)
	}
	if s.Path != n.Owns {
		return Spec{}, fmt.Errorf("spec path %q is not the owned path %q", s.Path, n.Owns)
	}
	return s, nil
}

func state(generation string) engine.State {
	return engine.State{Exists: true, Digest: foundation.Digest([]byte(generation)), Text: "generation " + generation}
}
