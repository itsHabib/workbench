//go:build unix

package local

import "syscall"

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

// cleanupDeadLeader probes the recorded process group without signaling.
// kill(-pgid, 0): ESRCH => clean; anything else => Uncertain (do not
// blind-kill a possibly-reused pgid).
func cleanupDeadLeader(alloc Allocation, id string) (CleanupResult, error) {
	if alloc.PGID <= 0 {
		return CleanupResult{}, nil
	}
	err := syscall.Kill(-alloc.PGID, 0)
	if err == nil || err == syscall.EPERM {
		return CleanupResult{Uncertain: true, AllocationID: id}, nil
	}
	if err == syscall.ESRCH {
		return CleanupResult{}, nil
	}
	return CleanupResult{Uncertain: true, AllocationID: id}, nil
}
