//go:build unix

package controller_test

import (
	"syscall"
	"testing"
)

func deadLeaderProbePGID(t *testing.T) int {
	t.Helper()
	pgid, err := syscall.Getpgid(0)
	if err != nil {
		t.Fatal(err)
	}
	return pgid
}
