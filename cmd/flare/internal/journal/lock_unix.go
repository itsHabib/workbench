//go:build !windows

package journal

import (
	"errors"
	"os"
	"syscall"
)

// lockFile takes a non-blocking exclusive flock on the open file. A lock already
// held by another process returns errLockHeld instead of blocking.
func lockFile(f *os.File) error {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return errLockHeld
	}
	return err
}
