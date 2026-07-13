//go:build unix

package controller

import (
	"os"
	"os/signal"
	"syscall"
)

// wakeController best-effort signals the live controller so it notices the
// cancel-request marker promptly. The marker remains authoritative; failure
// here only delays cancel until the next poll (cancelPollInterval).
func wakeController(pid int) error {
	return syscall.Kill(pid, syscall.SIGUSR1)
}

// ignoreCancelSignal drains SIGUSR1 so cancel's wake-up signal cannot kill
// the foreground controller. The cancel-request marker remains authoritative.
func ignoreCancelSignal() func() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ch:
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}
