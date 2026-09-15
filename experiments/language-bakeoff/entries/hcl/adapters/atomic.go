package adapters

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"
)

// createTemp opens a temp file beside abs so a later rename is atomic.
// rel names the target in errors.
func createTemp(abs, rel string) (*os.File, error) {
	f, err := os.CreateTemp(filepath.Dir(abs), "."+filepath.Base(abs)+".wb-*")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("parent directory of %s does not exist; declare a dir resource and reference it", rel)
	}
	if err != nil {
		return nil, adapter.RelError(rel, err)
	}
	return f, nil
}

// commit makes the temp file durable and renames it over abs.
func commit(tmp *os.File, abs string, mode fs.FileMode) error {
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close() // the sync error is the one to report
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), abs)
}

// existingMode is the permission bits of the regular file at abs, or 0644
// when there is none, so a replace never widens or narrows access.
func existingMode(abs string) fs.FileMode {
	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return 0o644
	}
	return info.Mode().Perm()
}

// removeTemp discards a temp file. After a successful rename the name no
// longer exists, so the error is expected and ignored.
func removeTemp(tmp *os.File) { _ = os.Remove(tmp.Name()) }

// writeAtomic replaces abs with data: readers see the old or new content,
// never a torn write.
func writeAtomic(abs, rel string, data []byte, mode fs.FileMode) error {
	tmp, err := createTemp(abs, rel)
	if err != nil {
		return err
	}
	defer removeTemp(tmp)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close() // the write error is the one to report
		return adapter.RelError(rel, err)
	}
	return adapter.RelError(rel, commit(tmp, abs, mode))
}
