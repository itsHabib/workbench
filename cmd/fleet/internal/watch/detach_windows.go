//go:build windows

package watch

import (
	"os/exec"
	"syscall"
)

// detach starts the watcher in a new process group with no console, so the hook's
// exit and the harness's console do not take it.
func detach(cmd *exec.Cmd) {
	const createNewProcessGroup = 0x00000200
	const detachedProcess = 0x00000008
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess}
}
