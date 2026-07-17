package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// The append critical section is guarded by an exclusive lock file —
// O_CREATE|O_EXCL is atomic on every filesystem Go targets, so it works
// across processes, not just goroutines.
const (
	lockRetry   = 2 * time.Millisecond
	lockTimeout = 10 * time.Second
	lockStale   = 30 * time.Second
)

// ErrLockTimeout fires when the log lock can't be acquired — a coded error so
// callers can distinguish contention from corruption.
var ErrLockTimeout = errors.New("state_lock_timeout")

// lock acquires the store's exclusive append lock, returning the release func.
// A lock older than lockStale is presumed abandoned by a dead process and
// taken over. Staleness reads the store's injected clock, so a test can drive
// takeover deterministically instead of sleeping past lockStale.
//
// Every acquisition error retries until the deadline, not just EEXIST: on
// Windows, a concurrent release (os.Remove) puts the file in delete-pending
// state and a racing create fails ACCESS_DENIED — transient contention that a
// naive "unexpected error" branch misreads as fatal, silently dropping writes
// under load.
func (s *Store) lock() (func(), error) {
	path := filepath.Join(s.dir, "log.lock")
	// The timeout bounds real elapsed wall time, not the injected clock: the
	// retry loop advances via time.Sleep, so a frozen test clock would never
	// cross an injected-clock deadline and contention would spin forever.
	// Staleness (s.stale, below) still reads the injected clock so a test can
	// drive takeover deterministically.
	start := time.Now()
	var lastErr error
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d %s", os.Getpid(), s.now().UTC().Format(time.RFC3339))
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		lastErr = err
		if os.IsExist(err) && s.stale(path) {
			os.Remove(path)
			continue
		}
		if time.Since(start) > lockTimeout {
			return nil, fmt.Errorf("%w after %s: %v", ErrLockTimeout, lockTimeout, lastErr)
		}
		time.Sleep(lockRetry)
	}
}

func (s *Store) stale(path string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	return s.now().Sub(fi.ModTime()) > lockStale
}
