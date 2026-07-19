//go:build !windows

package rooms

import (
	"os/exec"
	"syscall"
)

func setProcessGroup(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }

func processGroupID(cmd *exec.Cmd) (int, error) { return syscall.Getpgid(cmd.Process.Pid) }

func signalProcessGroup(cmd *exec.Cmd) error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }

func killProcessGroup(cmd *exec.Cmd) error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

func killDurableGroup(pgid int) error { return syscall.Kill(-pgid, syscall.SIGKILL) }
