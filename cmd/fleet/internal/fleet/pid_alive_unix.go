//go:build !windows && !darwin

package fleet

import "syscall"

// PidAlive is observed signal-zero liveness on other Unix hosts.
func PidAlive(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}
