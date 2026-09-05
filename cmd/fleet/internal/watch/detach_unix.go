//go:build !windows

package watch

import (
	"os/exec"
	"syscall"
)

// detach puts the watcher in its own session so the hook's exit does not take it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
