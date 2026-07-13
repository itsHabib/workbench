//go:build windows

package controller_test

import "testing"

func deadLeaderProbePGID(t *testing.T) int {
	t.Helper()
	// Windows cleanupDeadLeader always reports Uncertain; any positive pgid works.
	return 1
}
