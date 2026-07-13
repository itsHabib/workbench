//go:build windows

package local

import (
	"syscall"
)

func pidExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	// Mirror claim's STILL_ACTIVE check — no tasklist substring matching.
	const processQueryLimitedInformation = 0x1000
	const stillActive = 259
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}

// cleanupDeadLeader cannot enumerate a Windows process group after the
// leader is gone — report Uncertain rather than claiming clean.
func cleanupDeadLeader(alloc Allocation, id string) (CleanupResult, error) {
	return CleanupResult{Uncertain: true, AllocationID: id}, nil
}
