package fleet

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestSandboxedProcessLiveness(t *testing.T) {
	if raw := os.Getenv("FLEET_PID_PROBE"); raw != "" {
		checkSandboxLiveness(t, raw)
		return
	}
	sandbox, err := exec.LookPath("sandbox-exec")
	if err != nil {
		t.Skip("sandbox-exec unavailable")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sandbox, "-p", "(version 1)(allow default)(deny signal)", exe, "-test.run=^TestSandboxedProcessLiveness$")
	cmd.Env = append(os.Environ(), "FLEET_PID_PROBE="+strconv.Itoa(os.Getpid()))
	out, err := cmd.CombinedOutput()
	if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 71 && strings.Contains(string(out), "sandbox_apply") {
		t.Skip("already inside a sandbox; macOS refuses a nested sandbox-exec profile")
	}
	if err != nil {
		t.Fatalf("sandbox probe: %v\n%s", err, out)
	}
}

// Another user's live process answers EPERM without any sandbox. It keeps the
// documented cross-user rule: only this user's denied process reads as alive.
func TestProcessLivenessRejectsOtherUser(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root may signal every process")
	}
	if err := syscall.Kill(1, 0); !errors.Is(err, syscall.EPERM) {
		t.Skipf("launchd signal probe returned %v, not EPERM", err)
	}
	if PidAlive(1) {
		t.Fatal("another user's process read as this user's live session")
	}
	if PidGone(1) {
		t.Fatal("a live process was proven absent")
	}
}

func TestProcessLivenessRejectsAbsentPID(t *testing.T) {
	for _, pid := range []int{0, -1} {
		if PidAlive(pid) {
			t.Fatalf("invalid pid %d accepted", pid)
		}
	}
	cmd := exec.Command("/usr/bin/true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if PidAlive(cmd.Process.Pid) {
		t.Fatal("reaped process still alive")
	}
	if !PidGone(cmd.Process.Pid) {
		t.Fatal("reaped process not absent")
	}
}

func checkSandboxLiveness(t *testing.T, raw string) {
	t.Helper()
	pid, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("signal probe must be denied, got %v", err)
	}
	if !PidAlive(pid) {
		t.Fatal("sandbox classified live host process as dead")
	}
	if PidGone(pid) {
		t.Fatal("sandbox proved false absence")
	}
	if !SessionAlive(Rec{"pid": pid, "pid_kind": "harness"}) {
		t.Fatal("live harness session refused")
	}
	if SessionAlive(Rec{"pid": pid, "pid_kind": "harness", "ended": true}) {
		t.Fatal("ended session accepted")
	}
}
