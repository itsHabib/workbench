// Package adapters holds the adapters compiled into wb. Each one owns the
// real effects and observations for a single block type.
package adapters

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"unicode/utf8"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"
)

// maxDiffText bounds the content kept for readable plan diffs.
const maxDiffText = 4096

// File is a resource: a workspace file whose content is declared in source.
// Identity is its path. It owns that path while declared; wb never deletes it.
type File struct{}

// Type implements adapter.Adapter.
func (File) Type() string { return "file" }

// Kind implements adapter.Adapter.
func (File) Kind() adapter.Kind { return adapter.Resource }

// Fields implements adapter.Adapter.
func (File) Fields() []adapter.Field {
	return []adapter.Field{
		{Name: "path", Type: adapter.String, Required: true},
		{Name: "content", Type: adapter.String, Required: true},
	}
}

// Resolve implements adapter.Adapter. It derives sha256 so other blocks can
// reference the desired digest without an apply.
func (File) Resolve(cfg adapter.Values) (adapter.Values, error) {
	p, err := adapter.CleanPath(cfg.Str("path"))
	if err != nil {
		return nil, &adapter.FieldError{Field: "path", Msg: err.Error()}
	}
	content := cfg.Str("content")
	return adapter.Values{"path": p, "content": content, "sha256": adapter.DigestBytes([]byte(content))}, nil
}

// Paths implements adapter.Adapter.
func (File) Paths(v adapter.Values) adapter.Paths {
	return adapter.Paths{Owns: []string{v.Str("path")}}
}

// Want implements adapter.ResourceAdapter.
func (File) Want(v adapter.Values) adapter.Fact {
	return contentFact([]byte(v.Str("content")))
}

// Observe implements adapter.ResourceAdapter.
func (File) Observe(ws adapter.Workspace, v adapter.Values) (adapter.Fact, error) {
	abs, err := ws.Abs(v.Str("path"))
	if err != nil {
		return adapter.Fact{}, err
	}
	info, err := os.Lstat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return adapter.Fact{Fingerprint: adapter.Absent, Summary: "absent"}, nil
	}
	if err != nil {
		return adapter.Fact{}, adapter.RelError(v.Str("path"), err)
	}
	if !info.Mode().IsRegular() {
		return adapter.Fact{}, fmt.Errorf("%s exists and is not a regular file (%s); refusing to manage it", v.Str("path"), info.Mode().Type())
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return adapter.Fact{}, adapter.RelError(v.Str("path"), err)
	}
	return contentFact(b), nil
}

// Apply implements adapter.ResourceAdapter: an atomic replace, so repeating
// it is harmless. An existing file keeps its permission bits.
func (File) Apply(ws adapter.Workspace, v adapter.Values) error {
	abs, err := ws.Abs(v.Str("path"))
	if err != nil {
		return err
	}
	return writeAtomic(abs, v.Str("path"), []byte(v.Str("content")), existingMode(abs))
}

func contentFact(b []byte) adapter.Fact {
	fp := adapter.DigestBytes(b)
	f := adapter.Fact{
		Exists:      true,
		Fingerprint: fp,
		Summary:     fmt.Sprintf("%d bytes, sha256 %s", len(b), fp[len("sha256:"):][:12]),
	}
	if len(b) <= maxDiffText && utf8.Valid(b) {
		f.Text, f.HasText = string(b), true
	}
	return f
}
