package driverstate

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// This file is the lock MECHANISM shared by the lease, append, and import paths:
// one exclusive on-disk lock file, taken with the Windows delete-pending →
// retry-everything discipline, self-healing past a crashed holder. Policy (what
// a critical section does) lives in its callers; this layer only serializes.

// withLock runs fn while holding the exclusive lock file at path. It is the
// serialization primitive behind the per-run lease lock and the state-root
// import lock: acquire → fn → release, so a caller's whole read-check-mutate
// window is atomic against other holders of the same lock.
func withLock(path string, fn func() error) error {
	if err := acquireLock(path); err != nil {
		return err
	}
	defer releaseLock(path)
	return fn()
}

// acquireLock takes an exclusive lock via O_EXCL create. A lock orphaned by a
// crashed holder (mtime older than DefaultLeaseTTL) is broken and the create
// retried; a lost O_EXCL race or a Windows delete-pending permission failure is
// retried under the bounded budget. An exhausted budget surfaces errLockContended,
// never the internal errRetry marker (see withRetry).
func acquireLock(path string) error {
	return withRetry(func() error {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return f.Close()
		}
		if os.IsExist(err) {
			breakStaleLock(path)
			return errRetry
		}
		return fmt.Errorf("driverstate: acquire lock %s: %w", filepath.Base(path), err)
	})
}

func releaseLock(path string) { _ = os.Remove(path) }

// breakStaleLock removes a lock whose mtime is older than the lease TTL — an
// orphan from a writer that crashed mid-critical-section must not wedge the run
// forever. A fresh lock (a live holder) is left untouched. Best-effort: a lost
// race to remove it just means another attempt retries.
func breakStaleLock(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if time.Since(info.ModTime()) < DefaultLeaseTTL {
		return
	}
	_ = os.Remove(path)
}
