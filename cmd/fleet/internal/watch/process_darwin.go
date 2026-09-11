package watch

import (
	"fmt"
	"golang.org/x/sys/unix"
)

func processIdentity(pid int) (string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	if info == nil || info.Proc.P_pid != int32(pid) {
		return "", fmt.Errorf("process is absent")
	}
	return fmt.Sprintf("%d:%d", info.Proc.P_starttime.Sec, info.Proc.P_starttime.Usec), nil
}
