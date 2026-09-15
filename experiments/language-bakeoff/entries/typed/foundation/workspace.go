// Package foundation is the operational layer. It owns every real effect
// (writing files, running child processes, creating and deleting
// directories), the observations of what currently exists, and the
// evidence journal that records what ran. It knows nothing about graphs,
// intents or plans: the typed API's engine and the direct-tools baseline
// are both ordinary clients of it.
package foundation

import (
	"crypto/rand"
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

// reservedDir holds the evidence journal. Declared paths may not touch it.
const reservedDir = ".wb"

// Workspace confines effects to one directory. Paths are workspace-relative
// and slash-separated. Every file operation goes through os.Root, which
// rejects any path, including one reached through a symlink, that would
// leave the directory. Child processes are not confined (see Exec).
type Workspace struct {
	dir   string
	root  *os.Root
	fault Fault
}

// Open opens an existing directory as a workspace.
func Open(dir string, fault Fault) (*Workspace, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("workspace %s: %w", dir, err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, fmt.Errorf("open workspace: %w", err)
	}
	return &Workspace{dir: abs, root: root, fault: fault}, nil
}

// Close releases the workspace.
func (w *Workspace) Close() error { return w.root.Close() }

// Dir is the workspace's absolute directory.
func (w *Workspace) Dir() string { return w.dir }

// Digest is the content identity used across the system.
func Digest(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// FileState is an observation of one workspace file.
type FileState struct {
	Exists  bool
	Digest  string
	Content []byte
}

// ReadFile observes a file. A missing file is an observation, not an error.
func (w *Workspace) ReadFile(rel string) (FileState, error) {
	n, err := local(rel)
	if err != nil {
		return FileState{}, err
	}
	b, err := w.root.ReadFile(n)
	if errors.Is(err, fs.ErrNotExist) {
		return FileState{}, nil
	}
	if err != nil {
		return FileState{}, fmt.Errorf("read %s: %w", rel, err)
	}
	return FileState{Exists: true, Digest: Digest(b), Content: b}, nil
}

// WriteFile replaces a file's content atomically: the bytes go to a
// temporary sibling that is renamed into place, so a reader sees the old
// or the new content, never a torn write. Parent directories are created.
func (w *Workspace) WriteFile(rel string, content []byte) error {
	n, err := local(rel)
	if err != nil {
		return err
	}
	return w.commit(n, func(f *os.File) error {
		_, err := f.Write(content)
		return err
	})
}

// DirState is an observation of a path expected to hold a directory.
type DirState struct {
	Exists bool // something is at the path
	IsDir  bool // and it is a real directory, not a file or symlink
}

// StatDir observes a path without following a final symlink.
func (w *Workspace) StatDir(rel string) (DirState, error) {
	n, err := local(rel)
	if err != nil {
		return DirState{}, err
	}
	info, err := w.root.Lstat(n)
	if errors.Is(err, fs.ErrNotExist) {
		return DirState{}, nil
	}
	if err != nil {
		return DirState{}, fmt.Errorf("stat %s: %w", rel, err)
	}
	return DirState{Exists: true, IsDir: info.IsDir()}, nil
}

// DirEmpty reports whether a directory has no entries.
func (w *Workspace) DirEmpty(rel string) (bool, error) {
	n, err := local(rel)
	if err != nil {
		return false, err
	}
	f, err := w.root.Open(n)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", rel, err)
	}
	defer func() { _ = f.Close() }()
	names, err := f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return len(names) == 0, err
}

// MakeDir creates a directory and any missing parents.
func (w *Workspace) MakeDir(rel string) error {
	n, err := local(rel)
	if err != nil {
		return err
	}
	return w.root.MkdirAll(n, 0o755)
}

// Remove deletes a file or an empty directory. A non-empty directory is
// an error, so Remove never deletes contents.
func (w *Workspace) Remove(rel string) error {
	n, err := local(rel)
	if err != nil {
		return err
	}
	return w.root.Remove(n)
}

// Rename moves a file or directory within the workspace. Like os.Rename,
// it never replaces an existing directory.
func (w *Workspace) Rename(oldRel, newRel string) error {
	from, err := local(oldRel)
	if err != nil {
		return err
	}
	to, err := local(newRel)
	if err != nil {
		return err
	}
	return w.root.Rename(from, to)
}

// RemoveAll deletes a path and everything under it.
func (w *Workspace) RemoveAll(rel string) error {
	n, err := local(rel)
	if err != nil {
		return err
	}
	return w.root.RemoveAll(n)
}

// commit fills a temporary sibling of n and renames it over n.
func (w *Workspace) commit(n string, fill func(*os.File) error) error {
	if err := w.root.MkdirAll(filepath.Dir(n), 0o755); err != nil {
		return fmt.Errorf("create parent of %s: %w", n, err)
	}
	tmp := filepath.Join(filepath.Dir(n), "."+filepath.Base(n)+".wb-tmp-"+suffix())
	f, err := w.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("create temporary for %s: %w", n, err)
	}
	err = errors.Join(fill(f), f.Close())
	if err != nil {
		_ = w.root.Remove(tmp)
		return err
	}
	if err := w.root.Rename(tmp, n); err != nil {
		_ = w.root.Remove(tmp)
		return fmt.Errorf("commit %s: %w", n, err)
	}
	return nil
}

// local validates a workspace-relative path and converts it to an os.Root
// name. os.Root enforces confinement; this rejects the workspace root
// itself and unclean spellings, and reserves the evidence directory in
// any letter case, since on a case-insensitive filesystem ".WB" is ".wb".
func local(rel string) (string, error) {
	if rel == "" || rel == "." || path.Clean(rel) != rel || !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", fmt.Errorf("path %q must be a clean relative path inside the workspace", rel)
	}
	first, _, _ := strings.Cut(rel, "/")
	if strings.EqualFold(first, reservedDir) {
		return "", fmt.Errorf("path %q is reserved for evidence", rel)
	}
	return filepath.FromSlash(rel), nil
}

func suffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
