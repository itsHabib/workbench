// Package filelock is an exclusive advisory file lock, one implementation per
// operating system.
//
// Go's standard library has no portable file lock, and the two families differ
// in more than spelling: flock is whole-file and keyed to the descriptor, while
// Windows locks a byte range against the handle. Three tools had each grown
// their own copy of that knowledge, and one of them was never written for
// Windows at all.
//
// org and codexguard share this package. flare deliberately does not: its scoped
// design admits contracts as its sole in-repo dependency, and a leaf mechanism is
// still a second edge, so it keeps a private copy. The boundary is worth more
// than the duplication it costs.
//
// Callers own the file. This package neither opens nor closes it, because the
// call sites disagree about what the locked file is for: one opens a dedicated
// lock file beside the data, and one locks the very file it appends to. Wrapping
// open-lock-release here would fit one of them and fight the other.
package filelock

import "errors"

// ErrHeld reports that another process holds the lock. Only TryLock returns it.
// Lock waits instead, so it never does.
var ErrHeld = errors.New("filelock: held by another process")
