package filelock_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/filelock"
)

// TestTryLockReportsHeld is the one behaviour with logic rather than a single
// syscall: contention has to arrive as ErrHeld on both platforms, which they
// spell differently (EWOULDBLOCK against ERROR_LOCK_VIOLATION). Two handles to
// one path contend even inside a single process, so the test needs no subprocess.
func TestTryLockReportsHeld(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lock")

	holder := open(t, path)
	if err := filelock.Lock(holder); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	contender := open(t, path)
	if err := filelock.TryLock(contender); !errors.Is(err, filelock.ErrHeld) {
		t.Fatalf("TryLock on a held lock = %v, want ErrHeld", err)
	}

	if err := filelock.Unlock(holder); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if err := filelock.TryLock(contender); err != nil {
		t.Fatalf("TryLock after release = %v, want nil", err)
	}
}

func open(t *testing.T, path string) *os.File {
	t.Helper()

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { f.Close() })
	return f
}
