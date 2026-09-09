//go:build windows

package watch

import (
	"os/exec"
	"testing"
)

func TestDetachHasNoConsoleWindow(t *testing.T) {
	cmd := exec.Command("unused")
	detach(cmd)
	const noWindow = 0x08000000
	const detachedProcess = 0x00000008
	const newGroup = 0x00000200
	if cmd.SysProcAttr == nil {
		t.Fatal("missing process attributes")
	}
	flags := cmd.SysProcAttr.CreationFlags
	if flags&noWindow == 0 || flags&newGroup == 0 || flags&detachedProcess != 0 {
		t.Fatalf("unexpected creation flags: %#x", flags)
	}
}
