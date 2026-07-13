package controller

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/itsHabib/workbench/cmd/runway/internal/journal"
	"github.com/itsHabib/workbench/cmd/runway/internal/state"
	"github.com/itsHabib/workbench/contracts/execution"
)

// terminalGate is the single in-process exclusive transition point: exactly
// one of normal completion / cancellation / deadline wins the terminal write
// (Flow D).
type terminalGate struct {
	mu   sync.Mutex
	done bool
	res  execution.Result
}

// commit writes result.json via temp+Sync+rename, then appends run_terminal.
// A second caller observes the existing result and does not clobber.
func (g *terminalGate) commit(run state.RunDir, j *journal.Writer, res execution.Result) (execution.Result, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.done {
		return g.res, false, nil
	}
	if existing, ok, err := readResultIfPresent(run); err != nil {
		return execution.Result{}, false, err
	} else if ok {
		g.done = true
		g.res = existing
		return existing, false, nil
	}
	if err := execution.ValidateResult(res); err != nil {
		return execution.Result{}, false, fmt.Errorf("controller: invalid result before rename: %w", err)
	}
	if err := writeResultAtomic(run.ResultPath(), res); err != nil {
		return execution.Result{}, false, err
	}
	details := map[string]any{
		"status":         res.Status,
		"terminal_phase": res.TerminalPhase,
		"reason_code":    res.ReasonCode,
	}
	if _, err := j.Append(execution.PhaseTerminal, execution.KindRunTerminal, details); err != nil {
		return execution.Result{}, false, err
	}
	g.done = true
	g.res = res
	return res, true, nil
}

func writeResultAtomic(path string, res execution.Result) error {
	data, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("controller: encode result: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "result.json.*")
	if err != nil {
		return fmt.Errorf("controller: create result temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("controller: write result temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("controller: sync result temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("controller: close result temp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		cleanup()
		return fmt.Errorf("controller: chmod result temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("controller: rename result: %w", err)
	}
	return nil
}
