package local_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend"
	"github.com/itsHabib/workbench/cmd/runway/internal/backend/local"
)

func TestRedactionAcrossChunks(t *testing.T) {
	dir := t.TempDir()
	secret := "s3cr3t-VALUE-42"
	prog := filepath.Join(dir, "echo_secret.go")
	src := `package main
import ("os"; "time")
func main() {
	s := os.Getenv("FIXTURE_SECRET")
	os.Stdout.Write([]byte(s[:len(s)/2]))
	time.Sleep(20 * time.Millisecond)
	os.Stdout.Write([]byte(s[len(s)/2:]))
	os.Stdout.Write([]byte("\n"))
}
`
	if err := os.WriteFile(prog, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout := filepath.Join(dir, "stdout.log")
	stderr := filepath.Join(dir, "stderr.log")
	be := local.New()
	ctx := context.Background()
	emit := func(_, _ string, _ map[string]any) error { return nil }
	env := append(os.Environ(), "FIXTURE_SECRET="+secret)
	h, err := be.Start(ctx, backend.PreparedRun{
		Cwd:        dir,
		Argv:       []string{"go", "run", prog},
		Env:        env,
		StdoutPath: stdout,
		StderrPath: stderr,
		Secrets:    [][]byte{[]byte(secret)},
	}, emit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := be.Wait(ctx, h, emit); err != nil {
		t.Fatal(err)
	}
	_ = be.Cleanup(ctx, h)

	out, err := os.ReadFile(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(out, []byte(secret)) {
		t.Fatalf("secret leaked into stdout.log: %q", out)
	}
	if !bytes.Contains(out, []byte("[REDACTED]")) {
		t.Fatalf("expected [REDACTED] in stdout, got %q", out)
	}
	if err := scanForSecret(dir, secret); err != nil {
		t.Fatal(err)
	}
}

func TestCaptureBackgroundChildInheritingStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("background grandchild fixture uses unix process groups")
	}
	dir := t.TempDir()
	prog := filepath.Join(dir, "bg_stdout.go")
	src := `package main
import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"
)
func main() {
	cmd := exec.Command("sh", "-c", "echo bg-child-line; sleep 60")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	_ = os.WriteFile(os.Args[1], []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	fmt.Println("parent-line")
	time.Sleep(50 * time.Millisecond)
}
`
	if err := os.WriteFile(prog, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	childPIDFile := filepath.Join(dir, "child.pid")
	stdout := filepath.Join(dir, "stdout.log")
	stderr := filepath.Join(dir, "stderr.log")
	be := local.New()
	ctx := context.Background()
	emit := func(_, _ string, _ map[string]any) error { return nil }
	h, err := be.Start(ctx, backend.PreparedRun{
		Cwd:        dir,
		Argv:       []string{"go", "run", prog, childPIDFile},
		Env:        os.Environ(),
		StdoutPath: stdout,
		StderrPath: stderr,
	}, emit)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := be.Wait(ctx, h, emit)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Wait hung — grandchild inheriting stdout defeated EOF drain")
	}
	out, err := os.ReadFile(stdout)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("parent-line")) {
		t.Fatalf("parent stdout missing: %q", out)
	}
	raw, err := os.ReadFile(childPIDFile)
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if err := be.Cleanup(ctx, h); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(childPID) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("orphan grandchild pid %d still alive after cleanup", childPID)
}

func TestEmitsWorkloadReady(t *testing.T) {
	dir := t.TempDir()
	stdout := filepath.Join(dir, "stdout.log")
	stderr := filepath.Join(dir, "stderr.log")
	be := local.New()
	ctx := context.Background()
	var kinds []string
	emit := func(_, kind string, _ map[string]any) error {
		kinds = append(kinds, kind)
		return nil
	}
	h, err := be.Start(ctx, backend.PreparedRun{
		Cwd: dir,
		// "go version" is a cross-platform no-op workload; "true" has no
		// Windows analog on PATH.
		Argv:       []string{"go", "version"},
		Env:        os.Environ(),
		StdoutPath: stdout,
		StderrPath: stderr,
	}, emit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := be.Wait(ctx, h, emit); err != nil {
		t.Fatal(err)
	}
	_ = be.Cleanup(ctx, h)
	want := []string{"placement_allocated", "workload_ready", "workload_started", "workload_exited"}
	if len(kinds) != len(want) {
		t.Fatalf("kinds=%v want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds[%d]=%q want %q (full=%v)", i, kinds[i], want[i], kinds)
		}
	}
}

func TestNoOrphanProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("orphan process-group assertion uses kill -0")
	}
	dir := t.TempDir()
	prog := filepath.Join(dir, "spawn.go")
	src := `package main
import (
	"os"
	"os/exec"
	"strconv"
	"time"
)
func main() {
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	_ = os.WriteFile(os.Args[1], []byte(strconv.Itoa(cmd.Process.Pid)), 0o600)
	time.Sleep(150 * time.Millisecond)
}
`
	if err := os.WriteFile(prog, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	childPIDFile := filepath.Join(dir, "child.pid")
	stdout := filepath.Join(dir, "stdout.log")
	stderr := filepath.Join(dir, "stderr.log")
	be := local.New()
	ctx := context.Background()
	emit := func(_, _ string, _ map[string]any) error { return nil }
	h, err := be.Start(ctx, backend.PreparedRun{
		Cwd:        dir,
		Argv:       []string{"go", "run", prog, childPIDFile},
		Env:        os.Environ(),
		StdoutPath: stdout,
		StderrPath: stderr,
	}, emit)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := be.Wait(ctx, h, emit); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(childPIDFile)
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if err := be.Cleanup(ctx, h); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(childPID) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("orphan child pid %d still alive after cleanup", childPID)
}

func pidAlive(pid int) bool {
	err := exec.Command("kill", "-0", strconv.Itoa(pid)).Run()
	return err == nil
}

func scanForSecret(root, secret string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte(secret)) {
			return &secretLeak{path: path}
		}
		return nil
	})
}

type secretLeak struct{ path string }

func (s *secretLeak) Error() string { return "secret bytes found in " + s.path }
