// Package foundation owns every real effect and observation wb makes: one
// workspace directory confined with os.Root, content digests, and the
// append-only journal that retains evidence of what ran. It knows nothing
// about the language or the planner; they are clients of it.
package foundation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Digests for paths that are not regular files.
const (
	Absent = "absent"
	Dir    = "dir"
	Other  = "other"
)

// Names wb reserves inside a workspace. Declarations may not claim them.
const (
	StateDir   = ".wb"         // holds the journal
	TempSuffix = ".wb-partial" // temporary file beside a path being written
)

// Workspace is the only directory wb touches. Every file effect goes through
// an os.Root, so neither ".." nor a symlink can carry an effect outside it.
type Workspace struct {
	root *os.Root
	dir  string
}

// Open opens an existing workspace directory. wb never creates the
// workspace itself, so a mistyped path fails instead of growing new files.
func Open(dir string) (*Workspace, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("workspace %s: %w", dir, err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("workspace %s: %w", dir, err)
	}
	return &Workspace{root: root, dir: abs}, nil
}

// Close releases the workspace handle. Closing a directory handle cannot
// lose data, so there is no error to report.
func (w *Workspace) Close() { _ = w.root.Close() }

// Root is the confined file-system handle adapters use for effects.
func (w *Workspace) Root() *os.Root { return w.root }

// Dir is the absolute workspace path, for child-process working directories.
func (w *Workspace) Dir() string { return w.dir }

// Observe reports what exists at path itself, without following a
// symlink: Absent, Dir, Other, or the sha256 digest of a regular file's
// content along with its size. It describes paths wb may write, so a path
// reached through a symlinked parent directory is an error.
func (w *Workspace) Observe(path string) (digest string, size int64, err error) {
	if err := w.realParents(path); err != nil {
		return "", 0, err
	}
	info, err := w.root.Lstat(path)
	return w.describe(path, info, err)
}

// realParents refuses a path whose parent directories include a symlink.
// os.Root keeps links inside the workspace, but a link inside it could
// still carry a write into another declaration's path or into .wb, so wb
// writes only through real directories.
func (w *Workspace) realParents(path string) error {
	for dir := filepath.Dir(path); dir != "." && dir != string(filepath.Separator); dir = filepath.Dir(dir) {
		info, err := w.root.Lstat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s: parent %s is a symlink; wb writes only through real directories", path, dir)
		}
	}
	return nil
}

// ObserveTarget is Observe for a path wb only reads. It follows symlinks,
// so the digest changes when linked content changes; os.Root still refuses
// a link that leaves the workspace.
func (w *Workspace) ObserveTarget(path string) (digest string, size int64, err error) {
	info, err := w.root.Stat(path)
	return w.describe(path, info, err)
}

func (w *Workspace) describe(path string, info fs.FileInfo, err error) (string, int64, error) {
	if errors.Is(err, fs.ErrNotExist) {
		return Absent, 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	if info.IsDir() {
		return Dir, 0, nil
	}
	if !info.Mode().IsRegular() {
		return Other, 0, nil
	}
	f, err := w.root.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), n, nil
}

// CreateTemp opens a fresh temporary file beside path, creating missing
// parent directories (which wb does not own and never removes). Whatever
// already sits at the temporary name, such as a leftover from a crash or a
// planted symlink, is removed first, and the file is created exclusively,
// so a write can never follow a link.
func (w *Workspace) CreateTemp(path string) (*os.File, string, error) {
	tmp := path + TempSuffix
	if err := w.MkdirAll(filepath.Dir(path)); err != nil {
		return nil, "", err
	}
	if err := w.root.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, "", err
	}
	f, err := w.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, "", err
	}
	return f, tmp, nil
}

// MkdirAll creates a directory and its missing parents, refusing to create
// them through a symlinked parent.
func (w *Workspace) MkdirAll(path string) error {
	if err := w.realParents(filepath.Join(path, "x")); err != nil {
		return err
	}
	return w.root.MkdirAll(path, 0o755)
}

// Commit replaces path with a finished temporary file. On failure the
// temporary file is removed.
func (w *Workspace) Commit(tmp, path string) error {
	if err := w.root.Rename(tmp, path); err != nil {
		_ = w.root.Remove(tmp)
		return err
	}
	return nil
}

// Discard removes a temporary file that will not be committed.
func (w *Workspace) Discard(tmp string) { _ = w.root.Remove(tmp) }

// WriteFile replaces path with data atomically: a reader sees the old
// content or the new content, never a torn write.
func (w *Workspace) WriteFile(path string, data []byte) error {
	f, tmp, err := w.CreateTemp(path)
	if err != nil {
		return err
	}
	if err := syncClose(f, data); err != nil {
		w.Discard(tmp)
		return err
	}
	return w.Commit(tmp, path)
}

func syncClose(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// DigestBytes is the digest Observe would report for a file holding b.
func DigestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
