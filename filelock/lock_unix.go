//go:build !windows

package filelock

import (
	"errors"
	"os"
	"syscall"
)

// Lock takes an exclusive lock on the whole file, waiting until it is held.
func Lock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
}

// TryLock takes an exclusive lock without waiting, reporting ErrHeld when
// another process already holds it.
func TryLock(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return ErrHeld
	}
	return err
}

// Unlock releases a lock taken by Lock or TryLock. Closing the file also
// releases it; callers that close immediately need not unlock first.
func Unlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
