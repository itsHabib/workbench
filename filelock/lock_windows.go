//go:build windows

package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// Windows locks byte ranges rather than whole files, so every call names the
// same one-byte range at offset 0. A lock file is usually empty, and locking a
// range past the end is allowed.
const lockBytes = 1

// Lock takes an exclusive lock on the file, waiting until it is held.
// LockFileEx waits precisely because LOCKFILE_FAIL_IMMEDIATELY is absent.
func Lock(f *os.File) error {
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		lockBytes,
		0,
		new(windows.Overlapped),
	)
}

// TryLock takes an exclusive lock without waiting, reporting ErrHeld when
// another process already holds it. LOCKFILE_FAIL_IMMEDIATELY turns the wait
// into ERROR_LOCK_VIOLATION.
func TryLock(f *os.File) error {
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		lockBytes,
		0,
		new(windows.Overlapped),
	)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrHeld
	}
	return err
}

// Unlock releases a lock taken by Lock or TryLock. Closing the handle also
// releases it; callers that close immediately need not unlock first.
func Unlock(f *os.File) error {
	return windows.UnlockFileEx(
		windows.Handle(f.Fd()),
		0,
		lockBytes,
		0,
		new(windows.Overlapped),
	)
}
