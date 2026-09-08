//go:build windows

package watch

import (
	"os/exec"
	"syscall"
)

// detach starts the watcher in a new process group with no console window, so the
// hook's exit and the harness's console do not take it.
//
// CREATE_NO_WINDOW rather than DETACHED_PROCESS. Both keep the child off the parent's
// console, but `go build` produces a console-subsystem binary, and under
// DETACHED_PROCESS Windows gives such a child its own console - which is a window that
// pops on screen for every spawn. Since any session revives a dead watcher, a watcher
// that fails to stay up flashes a window per SessionStart. CREATE_NO_WINDOW is the flag
// that means "console application, no window"; it is documented as invalid combined
// with DETACHED_PROCESS, so this replaces it rather than adding to it.
func detach(cmd *exec.Cmd) {
	const createNewProcessGroup = 0x00000200
	const createNoWindow = 0x08000000
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | createNoWindow}
}
