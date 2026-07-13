//go:build linux

package controller

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// startTicks reads /proc/<pid>/stat field 22 (starttime). Absence of the
// proc entry means the process is gone. Same PID + different starttime means
// the recorded owner is dead and the PID was reused — the check PR 3's writer
// claim relies on.
func startTicks(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, fmt.Errorf("controller: read /proc/%d/stat: %w", pid, err)
	}
	s := string(data)
	// Comm may contain spaces/parens; field 1 ends at the last ") ".
	idx := strings.LastIndex(s, ") ")
	if idx < 0 {
		return 0, fmt.Errorf("controller: malformed /proc/%d/stat", pid)
	}
	fields := strings.Fields(s[idx+2:])
	// After comm: state(2) ... starttime is field 22 overall → index 19 here
	// because fields[0] is state (overall field 3).
	const startIdx = 19
	if len(fields) <= startIdx {
		return 0, fmt.Errorf("controller: short /proc/%d/stat", pid)
	}
	ticks, err := strconv.ParseUint(fields[startIdx], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("controller: parse starttime: %w", err)
	}
	return ticks, nil
}
