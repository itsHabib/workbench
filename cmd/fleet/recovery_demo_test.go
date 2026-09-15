package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestRecoveryDemo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local process and symlink demo targets macOS and Linux")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("Python 3 is required for the executable recovery demo")
	}
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "fleet")
	build := exec.Command("go", "build", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Fleet: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "examples/recovery/demo.py", bin)
	cmd.Env = append(os.Environ(), "TMPDIR="+tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("recovery demo: %v\n%s", err, out)
	}
}
