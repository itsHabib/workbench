package provider

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

func TestProviderProtocol(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executable uses a Unix shebang")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node is a provider prerequisite")
	}
	cmd := exec.Command("node", "--test", "runtime.test.mjs")
	if runtime.GOOS == "darwin" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Env = append(os.Environ(), "FLEET_TEST_OBSERVER="+exe)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("provider protocol: %v\n%s", err, out)
	}
}
