//go:build windows

package fleet

import (
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

const isWindows = true

// platformLongPath resolves an 8.3 component to its long form.
func platformLongPath(p string) string {
	u, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	buf := make([]uint16, 32767)
	n, err := windows.GetLongPathName(u, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) >= len(buf) {
		return p
	}
	return windows.UTF16ToString(buf[:n])
}

// platformNormCase is Python's os.path.normcase on Windows: lower-case, backslashes.
func platformNormCase(p string) string {
	return strings.ToLower(strings.ReplaceAll(p, "/", "\\"))
}

// PidAlive never uses a signal on Windows: that is TerminateProcess. It asks the
// process for its exit code and reads STILL_ACTIVE.
func PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const processQueryLimitedInformation = 0x1000
	h, err := windows.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}

// PidGone reports only proven process absence; an inspection error is unknown.
func PidGone(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_INVALID_PARAMETER)
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code != 259
}

// HarnessPid on Windows records the parent and says so, as the Python does without
// psutil. The reconciler falls back to turn_open plus age.
func HarnessPid(spawn bool) (int, string) {
	return os.Getppid(), "parent-unverified"
}
