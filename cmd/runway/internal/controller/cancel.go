package controller

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/journal"
	"github.com/itsHabib/workbench/cmd/runway/internal/state"
	"github.com/itsHabib/workbench/contracts/execution"
)

// CancelOutcome is the durable result of `runway cancel`.
type CancelOutcome struct {
	NoOp   bool
	Result *execution.Result // set when the run is already terminal
}

// RequestCancel writes the cancel-request marker atomically and signals the
// verified controller. Repetition after cancel-intent or terminal state is a
// successful no-op (Flow D / FR10).
func RequestCancel(stateRoot, runID string) (CancelOutcome, error) {
	run, err := state.Open(stateRoot, runID)
	if err != nil {
		return CancelOutcome{}, err
	}
	if res, ok, err := readResultIfPresent(run); err != nil {
		return CancelOutcome{}, err
	} else if ok {
		return CancelOutcome{NoOp: true, Result: &res}, nil
	}
	if cancelRequested(run) {
		return CancelOutcome{NoOp: true}, nil
	}
	id, err := readIdentity(run)
	if err != nil {
		return CancelOutcome{}, err
	}
	if !liveMatches(id) {
		return CancelOutcome{}, fmt.Errorf("controller: recorded controller identity is not live")
	}
	if err := writeCancelMarker(run); err != nil {
		return CancelOutcome{}, err
	}
	// Best-effort wake; the controller also polls the marker.
	_ = syscall.Kill(id.PID, syscall.SIGUSR1)
	return CancelOutcome{}, nil
}

func writeCancelMarker(run state.RunDir) error {
	path := run.CancelRequestPath()
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "cancel.request.*")
	if err != nil {
		return fmt.Errorf("controller: create cancel marker: %w", err)
	}
	tmpName := tmp.Name()
	payload := []byte(time.Now().UTC().Format(time.RFC3339Nano) + "\n")
	if _, err := tmp.Write(payload); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("controller: write cancel marker: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("controller: sync cancel marker: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("controller: close cancel marker: %w", err)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("controller: chmod cancel marker: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("controller: rename cancel marker: %w", err)
	}
	return nil
}

func cancelRequested(run state.RunDir) bool {
	_, err := os.Stat(run.CancelRequestPath())
	return err == nil
}

func readResultIfPresent(run state.RunDir) (execution.Result, bool, error) {
	data, err := os.ReadFile(run.ResultPath())
	if err != nil {
		if os.IsNotExist(err) {
			return execution.Result{}, false, nil
		}
		return execution.Result{}, false, fmt.Errorf("controller: read result: %w", err)
	}
	res, err := execution.DecodeResult(data)
	if err != nil {
		return execution.Result{}, false, err
	}
	if err := execution.ValidateResult(res); err != nil {
		return execution.Result{}, false, err
	}
	return res, true, nil
}

// HistoryTerminal reports whether the durable journal already ends in
// run_terminal — used by cancel no-op detection when result.json races.
func HistoryTerminal(run state.RunDir) (bool, error) {
	events, err := journal.ReadHistory(run.EventsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	st, err := execution.Reduce(events)
	if err != nil {
		return false, err
	}
	return st.Terminal, nil
}
