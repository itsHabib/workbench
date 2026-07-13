package controller

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/itsHabib/workbench/cmd/runway/internal/claim"
	"github.com/itsHabib/workbench/cmd/runway/internal/state"
)

// Identity is the recorded controller process-start identity. Cancel verifies
// it before signaling; reconcile verifies the same primitive against PID reuse.
// Do not treat PID alone as exclusivity — that is the writer claim.
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
// identity recorded at controller start. StartTicks 0 means the identity was
// recorded on a platform without a start-time source (unix && !linux) — an
// unverifiable identity fails closed: cancel never signals on a bare PID.
func liveMatches(id Identity) bool {
	if id.StartTicks == 0 {
		return false
	}
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
	ticks, err := claim.StartTicks(pid)
	if err != nil {
		return Identity{}, err
	}
	return Identity{PID: pid, StartTicks: ticks}, nil
}
