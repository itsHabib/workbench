// Package filelock is an exclusive advisory file lock, one implementation per
// operating system.
//
// Go's standard library has no portable file lock, and the two families differ
// in more than spelling: flock is whole-file and keyed to the descriptor, while
// Windows locks a byte range against the handle. This package is the only place
// in the repository that knows the difference. Three tools needed it and each
// had grown its own copy, one of which was never written for Windows at all.
//
// Callers own the file. This package neither opens nor closes it, because the
// three call sites disagree about what the locked file is for: two open a
// dedicated lock file beside the data, and one locks the very file it appends
// to. Wrapping open-lock-release here would fit one of them and fight the other
// two.
package filelock

import "errors"

// ErrHeld reports that another process holds the lock. Only TryLock returns it.
// Lock waits instead, so it never does.
var ErrHeld = errors.New("filelock: held by another process")
