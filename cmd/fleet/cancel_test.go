package main

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCancelArityCannotStartWatcher(t *testing.T) {
	if os.Getenv("FLEET_TEST_CANCEL_CHILD") == "1" {
		runWatch(os.Args[3:])
		return
	}
	for _, args := range [][]string{{"cancel"}, {"cancel", "address", "extra"}} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(ctx, exe, append([]string{"-test.run=^TestCancelArityCannotStartWatcher$", "--"}, args...)...)
		cmd.Env = append(os.Environ(), "FLEET_TEST_CANCEL_CHILD=1", "FLEET_STATE="+t.TempDir(), "ORG_STATE="+t.TempDir())
		out, err := cmd.CombinedOutput()
		cancel()
		if err == nil || cmd.ProcessState.ExitCode() != 2 || !strings.Contains(string(out), "usage: fleet watch cancel") {
			t.Fatalf("args %v: %v %s", args, err, out)
		}
	}
}
