//go:build windows

package local

import (
	"fmt"
	"os/exec"
	"strings"
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
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	// Exit 128 = process not found — already exited is success for cleanup,
	// mirroring unix ESRCH-only tolerance.
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 128 {
		return nil
	}
	return fmt.Errorf("local: taskkill: %w: %s", err, strings.TrimSpace(string(out)))
}
