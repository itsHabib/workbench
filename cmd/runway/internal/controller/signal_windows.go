//go:build windows

package controller

// wakeController is a no-op on Windows: there is no portable SIGUSR1 equivalent
// that the controller installs a drain for. Cancel remains marker-authoritative;
// latency is bounded by cancelPollInterval.
func wakeController(pid int) error {
	return nil
}

// ignoreCancelSignal returns a no-op cleanup on Windows (no wake signal to drain).
func ignoreCancelSignal() func() {
	return func() {}
}
