//go:build unix && !linux

package controller

import (
	"fmt"
	"runtime"
)

// startTicks is intentionally unsupported on non-Linux Unix (darwin, freebsd,
// …). The /proc starttime parser is Linux-only; Windows has its own
// creation-FILETIME identity. Compiling everywhere keeps the package portable;
// cancel identity verification fails closed at runtime on untested GOOS.
func startTicks(pid int) (uint64, error) {
	return 0, fmt.Errorf("controller: process-start identity unsupported on %s", runtime.GOOS)
}
