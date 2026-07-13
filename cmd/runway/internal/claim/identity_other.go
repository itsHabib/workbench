//go:build unix && !linux

package claim

// startTicks is unsupported on non-Linux Unix. Returning 0 keeps Acquire
// functional; LiveMatches falls back to pidExists so a live owner with an
// unverifiable start identity still yields ErrHeld on Takeover.
func startTicks(_ int) (uint64, error) {
	return 0, nil
}
