//go:build windows

package driverstate

import (
	"errors"
	"syscall"
)

// errorSharingViolation is Windows ERROR_SHARING_VIOLATION (32): a file is open
// by another process. It surfaces when a lock-free lease read collides with a
// concurrent atomic rename (Renew/steal) — Go does NOT fold it into
// os.ErrPermission, so it is matched explicitly. Transient by nature: the other
// handle closes within microseconds, so the read is retried.
const errorSharingViolation = syscall.Errno(32)

func isSharingViolation(err error) bool {
	var errno syscall.Errno
	return errors.As(err, &errno) && errno == errorSharingViolation
}
