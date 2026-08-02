//go:build unix

package local

import (
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// Bounds for waiting out a transient EPERM from killpg (see killGroup). The
// observed reap window is single-digit milliseconds; the budget is generous so
// a loaded machine still converges.
const (
	killGroupGrace = 500 * time.Millisecond
	killGroupPoll  = 10 * time.Millisecond
)

func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func processGroupID(cmd *exec.Cmd) (int, error) {
	if cmd.Process == nil {
		return 0, fmt.Errorf("process not started")
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return 0, err
	}
	return pgid, nil
}

func signalGroup(pgid int) error {
	if pgid <= 0 {
		return nil
	}
	return syscall.Kill(-pgid, syscall.SIGTERM)
}

// killGroup kills every member of pgid. Teardown only cares that nothing of
// ours is left running, so a group that is already gone counts as success.
//
// Darwin's killpg answers EPERM, not ESRCH, while the group's last members are
// zombies awaiting reap: no member can be signalled, yet the group id is still
// live. Nothing is running in that window and the reaper clears it within
// milliseconds, so wait it out rather than swallow EPERM — a genuine
// permission failure persists past the grace period and is still returned.
func killGroup(pgid int) error {
	if pgid <= 0 {
		return nil
	}
	deadline := time.Now().Add(killGroupGrace)
	for {
		err := syscall.Kill(-pgid, syscall.SIGKILL)
		// ESRCH means the group is already gone — success for cleanup.
		if err == nil || err == syscall.ESRCH {
			return nil
		}
		if err != syscall.EPERM || !time.Now().Before(deadline) {
			return err
		}
		time.Sleep(killGroupPoll)
	}
}
