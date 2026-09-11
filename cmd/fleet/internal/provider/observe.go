package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ProcessProof is evidence about one owned process, never a turn result.
// A no-fork lifetime proves there cannot be a surviving descendant.
type ProcessProof struct {
	Schema       string `json:"schema"`
	Attempt      string `json:"attempt"`
	Provider     string `json:"provider"`
	Method       string `json:"method"`
	PID          int    `json:"pid"`
	Armed        bool   `json:"armed"`
	ExecObserved bool   `json:"exec_observed"`
	ForkObserved bool   `json:"fork_observed"`
	ExitObserved bool   `json:"exit_observed"`
	NeverStarted bool   `json:"never_started"`
	Quiescent    bool   `json:"quiescent"`
	Error        string `json:"error,omitempty"`
}

// ObserveCommand is the private transport helper entry point. Its stdin/stdout
// belong to the provider. Only the attempt-specific proof file carries evidence.
func ObserveCommand(args []string) (int, error) {
	if len(args) < 3 || args[0] == "" || !filepath.IsAbs(args[1]) {
		return 2, fmt.Errorf("invalid provider observer arguments")
	}
	proof := ProcessProof{Schema: "fleet.process-proof.v1", Attempt: args[0], Provider: "codex", Method: "darwin-no-fork-v1"}
	code, err := observeProcess(args[2:], &proof)
	if err != nil {
		proof.Error = err.Error()
	}
	raw, writeErr := json.Marshal(proof)
	if writeErr != nil {
		return 1, writeErr
	}
	tmp := args[1] + ".tmp"
	if writeErr = os.WriteFile(tmp, append(raw, '\n'), 0600); writeErr != nil {
		return 1, writeErr
	}
	if writeErr = os.Rename(tmp, args[1]); writeErr != nil {
		return 1, writeErr
	}
	return code, err
}
