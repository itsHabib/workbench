package fleet

import (
	"errors"
	"os"
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
	// as proof of life or relaxing the caller's sandbox. Only a process of this
	// user can be a sandbox denial; another user's EPERM keeps reading as dead.
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	return err == nil && info != nil && int(info.Proc.P_pid) == pid && info.Eproc.Pcred.P_ruid == uint32(os.Getuid())
}
