//go:build windows

package controller

import (
	"fmt"
	"syscall"
)

// processQueryLimitedInformation is Win32 PROCESS_QUERY_LIMITED_INFORMATION.
// Package syscall does not export the constant; value matches processthreadsapi.h.
const processQueryLimitedInformation = 0x1000

// startTicks derives process-start identity from the creation FILETIME
// (64-bit count of 100ns intervals since 1601-01-01 UTC). Same PID + different
// creation time means the recorded owner is dead and the PID was reused — the
// Windows answer to TDD open question 4 that PR 3's writer claim verifies.
// Uses stdlib syscall only (no golang.org/x/sys).
func startTicks(pid int) (uint64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("controller: identity pid is invalid")
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return 0, fmt.Errorf("controller: open process %d: %w", pid, err)
	}
	defer syscall.CloseHandle(h)

	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0, fmt.Errorf("controller: get process times %d: %w", pid, err)
	}
	return uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime), nil
}
