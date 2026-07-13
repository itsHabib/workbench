//go:build unix && !linux

package claim

// startTicks is unsupported on non-Linux Unix. Returning 0 keeps Acquire
// functional while LiveMatches treats the identity as unverifiable
// (fail-closed) — Takeover of a live owner is refused without trusting PID.
func startTicks(_ int) (uint64, error) {
	return 0, nil
}
