//go:build windows

package claim

import "syscall"

// pidExists reports whether pid refers to a still-running process via
// OpenProcess + GetExitCodeProcess == STILL_ACTIVE. No tasklist substring
// matching — that is ambiguous and not a claim primitive.
func pidExists(pid int) bool {
	if pid <= 0 {
		return false
	}
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
