//go:build linux

package claim

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// startTicks reads /proc/<pid>/stat field 22 (starttime). Absence means the
// process is gone. Same PID + different starttime means the recorded owner is
// dead and the PID was reused.
func startTicks(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, fmt.Errorf("claim: read /proc/%d/stat: %w", pid, err)
	}
	s := string(data)
	idx := strings.LastIndex(s, ") ")
	if idx < 0 {
		return 0, fmt.Errorf("claim: malformed /proc/%d/stat", pid)
	}
	fields := strings.Fields(s[idx+2:])
	const startIdx = 19
	if len(fields) <= startIdx {
		return 0, fmt.Errorf("claim: short /proc/%d/stat", pid)
	}
	ticks, err := strconv.ParseUint(fields[startIdx], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("claim: parse starttime: %w", err)
	}
	return ticks, nil
}
