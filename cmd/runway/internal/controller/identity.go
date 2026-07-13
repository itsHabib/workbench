package controller

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/itsHabib/workbench/cmd/runway/internal/state"
)

// Identity is the recorded controller process identity used by cancel to
// verify the live owner before signaling (TDD §6 / Flow D).
type Identity struct {
	PID        int    `json:"pid"`
	StartTicks uint64 `json:"start_ticks"`
}

// writeIdentity persists this process's identity under private/controller.json.
func writeIdentity(run state.RunDir) (Identity, error) {
	id, err := selfIdentity()
	if err != nil {
		return Identity{}, err
	}
	data, err := json.Marshal(id)
	if err != nil {
		return Identity{}, fmt.Errorf("controller: encode identity: %w", err)
	}
	if err := run.WritePrivate("controller.json", data); err != nil {
		return Identity{}, err
	}
	return id, nil
}

// readIdentity loads the recorded controller identity for a run.
func readIdentity(run state.RunDir) (Identity, error) {
	data, err := os.ReadFile(run.ControllerPath())
	if err != nil {
		return Identity{}, fmt.Errorf("controller: read identity: %w", err)
	}
	var id Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return Identity{}, fmt.Errorf("controller: decode identity: %w", err)
	}
	if id.PID <= 0 {
		return Identity{}, fmt.Errorf("controller: identity pid is invalid")
	}
	return id, nil
}

// liveMatches reports whether pid still refers to the same process-start
// identity recorded at controller start.
func liveMatches(id Identity) bool {
	got, err := identityOf(id.PID)
	if err != nil {
		return false
	}
	return got.PID == id.PID && got.StartTicks == id.StartTicks
}

func selfIdentity() (Identity, error) {
	return identityOf(os.Getpid())
}

func identityOf(pid int) (Identity, error) {
	ticks, err := startTicks(pid)
	if err != nil {
		return Identity{}, err
	}
	return Identity{PID: pid, StartTicks: ticks}, nil
}

// startTicks reads /proc/<pid>/stat field 22 (starttime). Absence of the
// proc entry means the process is gone.
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
