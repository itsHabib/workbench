package adapter

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// StateDir is the engine's own directory inside a workspace. No block may
// own anything inside it.
const StateDir = ".wb"

// Absent is the fingerprint of a path that does not exist.
const Absent = "absent"

// Workspace confines every effect to one directory tree.
type Workspace struct{ root string }

// NewWorkspace roots a workspace at dir.
func NewWorkspace(dir string) (Workspace, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace: %w", err)
	}
	return Workspace{root: abs}, nil
}

// Root is the absolute workspace directory. Child processes run here.
func (w Workspace) Root() string { return w.root }

// Abs maps a workspace-relative path to an absolute path inside the root.
func (w Workspace) Abs(rel string) (string, error) {
	clean, err := CleanPath(rel)
	if err != nil {
		return "", err
	}
	return filepath.Join(w.root, filepath.FromSlash(clean)), nil
}

// Digest observes a workspace file: "sha256:<hex>", or Absent.
func (w Workspace) Digest(rel string) (string, error) {
	abs, err := w.Abs(rel)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return Absent, nil
	}
	if err != nil {
		return "", RelError(rel, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file (%s)", rel, info.Mode().Type())
	}
	f, err := os.Open(abs)
	if err != nil {
		return "", RelError(rel, err)
	}
	defer func() { _ = f.Close() }() // read-only: a close error loses nothing
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", RelError(rel, err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// RelError renames the path in a filesystem error to the workspace-relative
// one. Errors reach plans, terminal output and the journal; they should not
// carry the workspace's absolute location.
func RelError(rel string, err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return &fs.PathError{Op: pe.Op, Path: rel, Err: pe.Err}
	}
	var le *os.LinkError
	if errors.As(err, &le) {
		return &fs.PathError{Op: le.Op, Path: rel, Err: le.Err}
	}
	return err
}

// DigestBytes is the fingerprint Digest reports for a file holding b.
func DigestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// CleanPath validates a workspace-relative path and returns its canonical
// slash-separated form. Confinement is lexical: it rejects absolute paths,
// ".." escapes and the engine's state directory. It does not resolve
// symlinks.
func CleanPath(p string) (string, error) {
	if p == "" {
		return "", errors.New("path is empty")
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("path %q must be relative to the workspace", p)
	}
	c := path.Clean(filepath.ToSlash(p))
	if c == "." {
		return "", fmt.Errorf("path %q names the workspace root", p)
	}
	if c == ".." || strings.HasPrefix(c, "../") {
		return "", fmt.Errorf("path %q escapes the workspace", p)
	}
	// Case-insensitive: on macOS's default filesystem ".WB" is ".wb".
	if first, _, _ := strings.Cut(c, "/"); strings.EqualFold(first, StateDir) {
		return "", fmt.Errorf("path %q is inside the engine's %s directory", p, StateDir)
	}
	return c, nil
}
