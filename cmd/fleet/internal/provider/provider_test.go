package provider

import (
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
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("provider protocol: %v\n%s", err, out)
	}
}
