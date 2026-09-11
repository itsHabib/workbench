package fleet

import (
	stdcontext "context"
	"errors"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Subreaper status is process-wide. Keep it in a runner executing only this
// test, so it cannot adopt or wait on unrelated tests' children. This also works
// in containers whose PID 1 does not reap orphans.
func isolateCrashReplay(t *testing.T) bool {
	t.Helper()
	if os.Getenv("FLEET_CRASH_REPLAY") == "1" {
		if err := unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { reapCrashChildren(t) })
		return false
	}
	ctx, cancel := stdcontext.WithTimeout(stdcontext.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCrashReplacementModelTrace$", "-test.v")
	cmd.Env = append(os.Environ(), "FLEET_CRASH_REPLAY=1")
	out, err := cmd.CombinedOutput()
	t.Logf("isolated Linux replay:\n%s", out)
	if err != nil {
		t.Fatal(err)
	}
	return true
}

func reapCrashChildren(t *testing.T) {
	t.Helper()
	reaped := 0
	for {
		// Subtest cleanup has already closed the child sockets and waited on the
		// direct parents. Every remaining child is an adopted test descendant.
		_, err := unix.Wait4(-1, nil, 0, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if errors.Is(err, unix.ECHILD) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		reaped++
	}
	if !t.Failed() && reaped != 2 {
		t.Fatalf("reaped %d adopted children, want one per fixture (2)", reaped)
	}
	t.Logf("reaped %d adopted children; wait4 confirms no children remain", reaped)
}
