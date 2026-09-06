package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/itsHabib/workbench/filelock"
)

// ErrKeyBusy is a key's lock not taken in time. Always a refusal, never an authorization.
var ErrKeyBusy = errors.New("key busy")

// KeyLock is the one serialisation point for a key. Every mutation of a lease
// happens inside it.
//
// The same defect was found three times: a decision made by one process on state a
// second process could change, then acted on by pathname. Each fix serialised the
// one pair that had been found, and with N verbs there are N² pairs. This closes the
// class instead: inside the lock nothing else can touch the key, so read-decide-write
// is atomic and removing by pathname is safe again.
//
// Why a kernel lock and not a lock file. A lock whose existence IS the claim (an
// O_EXCL file, a mkdir, a token) survives the death of its holder, so recovery has to
// guess whether the holder is working or gone — a pid liveness check, which is exactly
// the guessing this substrate refuses. An advisory lock is owned by the open file
// description, and the kernel releases it when the process exits, including kill -9.
// Nothing to reclaim, nothing to strand, no pid.
//
// ADVISORY: this only holds while every writer takes it. A mutation added later that
// writes a lease outside KeyLock reopens the whole class. There is no enforcement;
// there is only this comment and the fact that every mutation in this package is
// inside one. Never call a locking verb from inside another for the same key: a
// second descriptor does not re-enter, so it blocks until the timeout and refuses.
//
// The lock FILE is never unlinked. On Windows an open file cannot be removed; on
// POSIX, unlinking it would let the next caller lock a different inode under the same
// name and two holders would both believe they were serialised.
//
// A holder that is merely slow (SIGSTOP, a pathological filesystem) makes this time
// out and the caller refuses: held-too-long, never held-twice.
func KeyLock(key string, fn func() error) error {
	return keyLockN(key, 60, 20*time.Millisecond, fn)
}

func keyLockN(key string, tries int, pause time.Duration, fn func() error) error {
	p := Path("keylocks", Safe(key)+".lock")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close() // releases the lock on both platforms
	locked := false
	for i := 0; i < tries; i++ {
		if err := filelock.TryLock(f); err == nil {
			locked = true
			break
		}
		time.Sleep(pause)
	}
	if !locked {
		return ErrKeyBusy
	}
	defer func() { _ = filelock.Unlock(f) }()
	return fn()
}
