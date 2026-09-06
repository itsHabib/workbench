//go:build !windows

package fleet

import (
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const isWindows = false

func platformLongPath(p string) string { return p }

// platformNormCase is the identity on POSIX, as os.path.normcase is.
func platformNormCase(p string) string { return p }

// PidAlive is liveness without side effects. Any error reads as dead — including
// EPERM — because that is what the Python's `except OSError` did, and a store the two
// share must judge a holder the same way.
func PidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}

// harnessRe matches the processes a hook can hang beneath: Claude Code (claude, or
// node/electron for the desktop app) and native Codex.
var harnessRe = regexp.MustCompile(`(?i)claude|codex|node|electron`)

// HarnessPid is the harness process that spawned this hook, best effort. Only when
// spawn is allowed (SessionStart, the one event where the one-spawn law does not
// bind) does it walk `ps -o ppid=,comm=` up from the parent, ten hops at most.
// Otherwise it records the parent and says so, and the reconciler falls back to
// turn_open plus age.
func HarnessPid(spawn bool) (int, string) {
	if spawn {
		if found := psWalk(os.Getppid(), 10); found > 0 {
			return found, "harness"
		}
	}
	return os.Getppid(), "parent-unverified"
}

func psWalk(pid, hops int) int {
	for i := 0; i < hops; i++ {
		if pid <= 1 {
			return 0
		}
		cmd := exec.Command("ps", "-o", "ppid=,comm=", "-p", strconv.Itoa(pid))
		done := make(chan struct{})
		var out []byte
		var err error
		go func() { out, err = cmd.Output(); close(done) }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			return 0
		}
		if err != nil {
			return 0
		}
		fields := strings.Fields(string(out))
		if len(fields) < 2 {
			return 0
		}
		comm := strings.Join(fields[1:], " ")
		if harnessRe.MatchString(baseName(comm)) {
			return pid
		}
		next, err := strconv.Atoi(fields[0])
		if err != nil {
			return 0
		}
		pid = next
	}
	return 0
}

func baseName(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
