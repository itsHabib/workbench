//go:build unix

package claim

import "syscall"

// pidExists reports whether pid refers to a live process. EPERM means the
// process exists but we lack permission to signal it — still alive.
func pidExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	return err == syscall.EPERM
}
