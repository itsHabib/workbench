//go:build windows

package rooms

import (
	"os"
	"os/exec"
)

func setProcessGroup(_ *exec.Cmd) {}

func processGroupID(cmd *exec.Cmd) (int, error) { return cmd.Process.Pid, nil }

func signalProcessGroup(cmd *exec.Cmd) error {
	if err := cmd.Process.Signal(os.Interrupt); err == nil {
		return nil
	}
	return cmd.Process.Kill()
}

func killProcessGroup(cmd *exec.Cmd) error { return cmd.Process.Kill() }

func killDurableGroup(pid int) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}
