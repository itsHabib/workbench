package controller

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/itsHabib/workbench/cmd/runway/internal/state"
)

// Identity is the recorded controller process-start identity. Cancel verifies
// it before signaling; PR 3's writer claim / reconcile will verify the same
// primitive against PID reuse (TDD open question 4 — Windows creation-time
// ticks vs Linux /proc starttime). Do not treat PID alone as exclusivity.
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
