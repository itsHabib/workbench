//go:build !linux && !windows

package fleet

import "testing"

// Non-Linux Unix hosts use their system init to reap the orphan. Linux needs
// the isolated subreaper because a container's PID 1 may not perform that job.
func isolateCrashReplay(_ *testing.T) bool { return false }
