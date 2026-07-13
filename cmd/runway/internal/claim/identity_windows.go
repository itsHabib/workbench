//go:build windows

package claim

import (
	"fmt"
	"syscall"
)

// processQueryLimitedInformation is Win32 PROCESS_QUERY_LIMITED_INFORMATION.
const processQueryLimitedInformation = 0x1000

// startTicks derives process-start identity from the creation FILETIME
// (64-bit count of 100ns intervals since 1601-01-01 UTC). Stdlib syscall
// only — resolves TDD open question #4 without golang.org/x/sys.
func startTicks(pid int) (uint64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("claim: identity pid is invalid")
	}
	h, err := syscall.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return 0, fmt.Errorf("claim: open process %d: %w", pid, err)
	}
	defer syscall.CloseHandle(h)

	// An exited process can still be opened while ANY handle to its object
	// remains (an unreaped child, a debugger, a tool). Creation time alone
	// would then report a dead owner as live — require STILL_ACTIVE too.
	var code uint32
	if err := syscall.GetExitCodeProcess(h, &code); err != nil {
		return 0, fmt.Errorf("claim: get exit code %d: %w", pid, err)
	}
	if code != stillActive {
		return 0, fmt.Errorf("claim: process %d has exited", pid)
	}

	var creation, exit, kernel, user syscall.Filetime
	if err := syscall.GetProcessTimes(h, &creation, &exit, &kernel, &user); err != nil {
		return 0, fmt.Errorf("claim: get process times %d: %w", pid, err)
	}
	return uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime), nil
}

// stillActive is Win32 STILL_ACTIVE: GetExitCodeProcess reports it for a
// running process. (A workload that deliberately exits with code 259 is a
// documented Windows footgun and out of scope.)
const stillActive = 259
