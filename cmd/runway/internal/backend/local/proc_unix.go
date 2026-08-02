//go:build unix

package local

import (
	"fmt"
	"os/exec"
	"syscall"
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func processGroupID(cmd *exec.Cmd) (int, error) {
	if cmd.Process == nil {
		return 0, fmt.Errorf("process not started")
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return 0, err
	}
	return pgid, nil
}

func signalGroup(pgid int) error {
	if pgid <= 0 {
		return nil
	}
	return syscall.Kill(-pgid, syscall.SIGTERM)
}

func killGroup(pgid int) error {
	if pgid <= 0 {
		return nil
	}
	err := syscall.Kill(-pgid, syscall.SIGKILL)
	if err == nil {
		return nil
	}
	// ESRCH means the group is already gone — success for cleanup.
	if err == syscall.ESRCH {
		return nil
	}
	// So does EPERM on Darwin. Cleanup runs killGroup a second time after Wait
	// already killed the group; by then the members are reaped-but-lingering,
	// and BSD kill(2) reports EPERM for them where Linux reports ESRCH. Both
	// mean the same thing here — nothing left to signal.
	if err == syscall.EPERM {
		return nil
	}
	return err
}
