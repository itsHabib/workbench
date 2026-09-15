// Package dir provides "keep dir": a disposable scratch directory. wb
// ensures it exists; its contents are not managed, so deleting it is safe
// and it is simply recreated. wb never deletes it or replaces a non-directory
// found at its path.
package dir

import (
	"fmt"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/intent"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/kind"
)

// Adapter is the dir kind.
type Adapter struct{}

// Schema declares the dir kind's attributes.
func (Adapter) Schema() kind.Schema {
	return kind.Schema{
		Kind:    "dir",
		Fields:  []kind.Field{{Name: "path", Type: kind.OwnedPath, Required: true}},
		Exports: []string{"path"},
	}
}

// Observe reports whether the directory exists.
func (Adapter) Observe(ws *foundation.Workspace, d intent.Decl) (kind.Fact, error) {
	return kind.ObservePath(ws, d.One("path"))
}

// Propose creates a missing directory and refuses to replace anything else.
// Its version is constant: contents never make dependent work stale.
func (Adapter) Propose(_ *foundation.Workspace, d intent.Decl, now kind.Fact) (kind.Proposal, error) {
	path := d.One("path")
	p := kind.Proposal{Version: foundation.Dir}
	switch now.Digest {
	case foundation.Dir:
		p.Action, p.Summary = kind.OK, path+"/ exists (contents unmanaged)"
	case foundation.Absent:
		p.Action, p.Summary = kind.Create, "create directory "+path+"/"
	default:
		p.Action, p.Summary = kind.Conflict, fmt.Sprintf("%s exists and is not a directory; wb will not replace it", path)
	}
	return p, nil
}

// Apply creates the directory and any missing parents.
func (Adapter) Apply(ws *foundation.Workspace, d intent.Decl) (kind.Fact, error) {
	path := d.One("path")
	if err := ws.MkdirAll(path); err != nil {
		return kind.Fact{}, err
	}
	return kind.ObservePath(ws, path)
}
