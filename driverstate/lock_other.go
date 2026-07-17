//go:build !windows

package driverstate

// isSharingViolation is Windows-only: POSIX rename is atomic and a concurrent
// read never fails this way, so there is nothing to match off Windows.
func isSharingViolation(_ error) bool { return false }
