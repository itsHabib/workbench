//go:build windows

package main

import (
	"fmt"
	"os"
)

// The fault harness kills and pauses processes with signals and is not
// built for Windows. The store itself is.
func planeMain([]string) int {
	fmt.Fprintln(os.Stderr, "swarm plane: the fault harness runs on unix only")
	return 3
}
