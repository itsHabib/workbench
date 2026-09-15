// Package file provides "keep file": a text artifact whose whole content
// wb owns. wb creates it, restores it when it drifts, and never deletes it.
package file

import (
	"fmt"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/intent"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/kind"
)

// Adapter is the file kind.
type Adapter struct{}

// Schema declares the file kind's attributes.
func (Adapter) Schema() kind.Schema {
	return kind.Schema{
		Kind: "file",
		Fields: []kind.Field{
			{Name: "path", Type: kind.OwnedPath, Required: true},
			{Name: "text", Type: kind.Text, Required: true},
		},
		Exports: []string{"path"},
	}
}

// Observe digests the file as it is now.
func (Adapter) Observe(ws *foundation.Workspace, d intent.Decl) (kind.Fact, error) {
	return kind.ObservePath(ws, d.One("path"))
}

// Propose compares the desired text with the file on disk.
func (Adapter) Propose(ws *foundation.Workspace, d intent.Decl, now kind.Fact) (kind.Proposal, error) {
	path, text := d.One("path"), d.One("text")
	p := kind.Proposal{Version: foundation.DigestBytes([]byte(text))}
	switch now.Digest {
	case p.Version:
		p.Action, p.Summary = kind.OK, fmt.Sprintf("%s matches (%d bytes)", path, len(text))
		return p, nil
	case foundation.Absent:
		p.Action, p.Summary = kind.Create, fmt.Sprintf("create %s (%d bytes)", path, len(text))
		return p, nil
	case foundation.Dir, foundation.Other:
		p.Action, p.Summary = kind.Conflict, fmt.Sprintf("%s exists and is not a regular file; wb will not replace it", path)
		return p, nil
	}
	old, err := ws.Root().ReadFile(path)
	if err != nil {
		return kind.Proposal{}, err
	}
	p.Action = kind.Update
	p.Summary = fmt.Sprintf("update %s (%d -> %d bytes)", path, len(old), len(text))
	p.Detail = LineDiff(string(old), text, 12)
	return p, nil
}

// Apply writes the desired text atomically, creating parent directories.
// Parents it creates are not owned and are never removed.
func (Adapter) Apply(ws *foundation.Workspace, d intent.Decl) (kind.Fact, error) {
	path := d.One("path")
	if err := ws.WriteFile(path, []byte(d.One("text"))); err != nil {
		return kind.Fact{}, err
	}
	return kind.ObservePath(ws, path)
}

// LineDiff shows how text changes: common leading and trailing lines are
// elided and the differing middle is shown as removed and added lines.
func LineDiff(before, after string, limit int) []string {
	a, b := lines(before), lines(after)
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	var out []string
	for _, l := range a {
		out = append(out, "-"+l)
	}
	for _, l := range b {
		out = append(out, "+"+l)
	}
	if len(out) == 0 {
		return []string{"(only the final newline differs)"}
	}
	if len(out) > limit {
		out = append(out[:limit], fmt.Sprintf("... %d more changed lines", len(out)-limit))
	}
	return out
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
