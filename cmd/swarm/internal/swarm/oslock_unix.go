//go:build !windows

package swarm

import (
	"os"

	"golang.org/x/sys/unix"
)

// tryLock takes an exclusive advisory lock on f without blocking. The
// kernel releases it when the holder's descriptor closes, including when
// the holder dies, so nobody ever has to guess a holder's death from age.
func tryLock(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == unix.EWOULDBLOCK {
		return false, nil
	}
	return err == nil, err
}

func unlock(f *os.File) { _ = unix.Flock(int(f.Fd()), unix.LOCK_UN) }
