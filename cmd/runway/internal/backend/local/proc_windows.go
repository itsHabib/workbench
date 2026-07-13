//go:build windows

package local

import (
	"fmt"
	"os/exec"
	"syscall"
)

const createNewProcessGroup = 0x00000200

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

func processGroupID(cmd *exec.Cmd) (int, error) {
	if cmd.Process == nil {
		return 0, fmt.Errorf("process not started")
	}
	return cmd.Process.Pid, nil
}

func signalGroup(pgid int) error {
	return killGroup(pgid)
}

func killGroup(pgid int) error {
	if pgid <= 0 {
		return nil
	}
	// taskkill /T ends the process tree rooted at the group leader.
	cmd := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pgid), "/T", "/F")
	if err := cmd.Run(); err != nil {
		// Already exited is fine for cleanup.
		return nil
	}
	return nil
}
