package journal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrWatchLocked reports that another flare process already holds the watch
// lock for this state dir. Two writers into the journal + cursors — a second
// watcher, or a sweep racing a watcher — is the exact condition that corrupts
// cursors.json, so the second instance must refuse rather than double-write.
var ErrWatchLocked = errors.New("journal: another flare instance holds the watch lock")

// errLockHeld is the per-OS "someone else has it" sentinel that lockFile
// returns; LockWatch translates it into the exported ErrWatchLocked.
var errLockHeld = errors.New("lock held")

func (j *Journal) lockPath() string { return filepath.Join(j.dir, "watch.lock") }

// LockWatch takes an exclusive, OS-level advisory lock scoped to this state dir
// so only one flare process mutates the journal + cursors at a time. The lock
// is held for the process lifetime and released by the returned func — and by
// the OS if the process dies, so a crash never leaves a stale lock to reap.
// Returns ErrWatchLocked when another live process already holds it.
func (j *Journal) LockWatch() (func() error, error) {
	f, err := os.OpenFile(j.lockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("journal: open lock: %w", err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		if errors.Is(err, errLockHeld) {
			return nil, ErrWatchLocked
		}
		return nil, fmt.Errorf("journal: lock: %w", err)
	}
	// Closing the file releases the OS lock; the caller holds f alive until then.
	return f.Close, nil
}
