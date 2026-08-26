//go:build windows

package journal

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes a non-blocking exclusive lock on the first byte of the open
// file via LockFileEx. When another process holds it, Windows returns
// ERROR_LOCK_VIOLATION (LOCKFILE_FAIL_IMMEDIATELY turns the block into an
// error), which maps to errLockHeld.
func lockFile(f *os.File) error {
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, new(windows.Overlapped),
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errLockHeld
	}
	return err
}
