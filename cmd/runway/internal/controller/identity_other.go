//go:build unix && !linux

package controller

// startTicks has no implementation on non-Linux Unix (darwin, freebsd, …):
// the /proc starttime parser is Linux-only and Windows uses its creation
// FILETIME. Returning the zero sentinel keeps `runway run` functional on
// these hosts while liveMatches treats an unverifiable identity as
// fail-closed — `runway cancel` (and PR 3's reconcile takeover) refuse
// rather than trust a bare PID. StartTicks 0 is unreachable for a real
// process on the supported platforms (Linux starttime and Windows creation
// FILETIME are never 0 for a userland process).
func startTicks(_ int) (uint64, error) {
	return 0, nil
}
