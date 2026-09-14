package fleet

import (
	"errors"
	"syscall"

	"golang.org/x/sys/unix"
)

// PidAlive observes a process without signaling it. A tool sandbox can refuse
// even signal zero against its live parent; that refusal alone is not death.
func PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err == nil {
		return true
	}
	if !errors.Is(err, syscall.EPERM) {
		return false
	}
	// Read the exact PID from the kernel, rather than treating denied inspection
	// as proof of life or relaxing the caller's sandbox.
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	return err == nil && info != nil && int(info.Proc.P_pid) == pid
}
