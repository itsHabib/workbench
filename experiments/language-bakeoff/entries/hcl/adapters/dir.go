package adapters

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"
)

// Dir is a resource: a directory other blocks write into. Identity is its
// path; its observed state is existence and permission bits, not content.
// It never replaces a non-directory and never deletes. An existing
// directory at its path is adopted; if its mode differs, the plan shows the
// chmod as an update before it happens. Blocks that write inside it must
// reference it: the compiler's generic ownership check enforces that from
// Paths alone.
type Dir struct{}

// Type implements adapter.Adapter.
func (Dir) Type() string { return "dir" }

// Kind implements adapter.Adapter.
func (Dir) Kind() adapter.Kind { return adapter.Resource }

// Fields implements adapter.Adapter.
func (Dir) Fields() []adapter.Field {
	return []adapter.Field{
		{Name: "path", Type: adapter.String, Required: true},
		{Name: "mode", Type: adapter.String}, // octal, default "0755"
	}
}

// Resolve implements adapter.Adapter.
func (Dir) Resolve(cfg adapter.Values) (adapter.Values, error) {
	p, err := adapter.CleanPath(cfg.Str("path"))
	if err != nil {
		return nil, &adapter.FieldError{Field: "path", Msg: err.Error()}
	}
	mode := cfg.Str("mode")
	if mode == "" {
		mode = "0755"
	}
	if _, err := parseMode(mode); err != nil {
		return nil, &adapter.FieldError{Field: "mode", Msg: err.Error()}
	}
	return adapter.Values{"path": p, "mode": mode}, nil
}

// Paths implements adapter.Adapter.
func (Dir) Paths(v adapter.Values) adapter.Paths {
	return adapter.Paths{Owns: []string{v.Str("path")}}
}

// Want implements adapter.ResourceAdapter.
func (Dir) Want(v adapter.Values) adapter.Fact {
	m, _ := parseMode(v.Str("mode"))
	return dirFact(m)
}

// Observe implements adapter.ResourceAdapter.
func (Dir) Observe(ws adapter.Workspace, v adapter.Values) (adapter.Fact, error) {
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
	if !info.IsDir() {
		return adapter.Fact{}, fmt.Errorf("%s exists and is not a directory; refusing to replace it", v.Str("path"))
	}
	return dirFact(info.Mode().Perm()), nil
}

// Apply implements adapter.ResourceAdapter.
func (Dir) Apply(ws adapter.Workspace, v adapter.Values) error {
	abs, err := ws.Abs(v.Str("path"))
	if err != nil {
		return err
	}
	m, err := parseMode(v.Str("mode"))
	if err != nil {
		return err
	}
	if err := os.Mkdir(abs, m); err != nil && !errors.Is(err, fs.ErrExist) {
		return adapter.RelError(v.Str("path"), err)
	}
	// Mkdir is subject to the umask; Chmod sets the declared bits exactly.
	return adapter.RelError(v.Str("path"), os.Chmod(abs, m))
}

func dirFact(m fs.FileMode) adapter.Fact {
	return adapter.Fact{
		Exists:      true,
		Fingerprint: fmt.Sprintf("dir mode %04o", uint32(m)),
		Summary:     fmt.Sprintf("directory, mode %04o", uint32(m)),
	}
}

func parseMode(s string) (fs.FileMode, error) {
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil || n > 0o777 {
		return 0, fmt.Errorf("mode %q must be octal permission bits such as \"0755\"", s)
	}
	return fs.FileMode(n), nil
}
