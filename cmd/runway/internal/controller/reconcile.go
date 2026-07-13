package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/itsHabib/workbench/cmd/runway/internal/backend/local"
	"github.com/itsHabib/workbench/cmd/runway/internal/claim"
	"github.com/itsHabib/workbench/cmd/runway/internal/journal"
	"github.com/itsHabib/workbench/cmd/runway/internal/state"
	"github.com/itsHabib/workbench/contracts/execution"
)

// ReconcileOutcome is the durable result of `runway reconcile` (Flow F).
type ReconcileOutcome struct {
	NoOp     bool
	Mutated  bool
	Owner    *claim.Owner // set when claim acquisition failed (another writer)
	Result   *execution.Result
	ExitCode int
}

// Reconcile repairs one known run after controller death (Flow F / TDD §8).
// It never scans for orphans. Concurrent reconcilers: exactly one mutates.
func Reconcile(stateRoot, runID string) (ReconcileOutcome, error) {
	run, err := state.Open(stateRoot, runID)
	if err != nil {
		return ReconcileOutcome{}, err
	}

	if out, done, err := reconcileAlreadyTerminal(run); done {
		return out, err
	}

	if err := assertControllerAbsentOrReused(run); err != nil {
		return ReconcileOutcome{}, err
	}

	owner, err := claim.Takeover(run.PrivateDir())
	if err != nil {
		return reconcileClaimLost(run, err)
	}
	_ = owner

	cleanup, err := local.CleanupDurable(run.PrivateDir())
	if err != nil {
		return ReconcileOutcome{}, err
	}

	if res, ok, err := readResultIfPresent(run); err != nil {
		return ReconcileOutcome{}, err
	} else if ok {
		return reconcileAppendTerminal(run, runID, res)
	}

	return reconcileControllerLost(run, runID, cleanup)
}

func reconcileAlreadyTerminal(run state.RunDir) (ReconcileOutcome, bool, error) {
	res, hasResult, err := readResultIfPresent(run)
	if err != nil {
		return ReconcileOutcome{}, true, err
	}
	term, err := HistoryTerminal(run)
	if err != nil {
		return ReconcileOutcome{}, true, err
	}
	if hasResult && term {
		return ReconcileOutcome{
			NoOp:     true,
			Result:   &res,
			ExitCode: ExitFromResult(res),
		}, true, nil
	}
	return ReconcileOutcome{}, false, nil
}

// assertControllerAbsentOrReused fails when the recorded controller identity
// is still live. Missing controller.json is "absent" and permits reconcile.
func assertControllerAbsentOrReused(run state.RunDir) error {
	id, err := readIdentity(run)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if liveMatches(id) {
		return fmt.Errorf("controller: recorded controller identity is still live")
	}
	return nil
}

func reconcileClaimLost(run state.RunDir, err error) (ReconcileOutcome, error) {
	if !errors.Is(err, claim.ErrHeld) && !errors.Is(err, claim.ErrBusy) {
		return ReconcileOutcome{}, err
	}
	out := ReconcileOutcome{NoOp: true, ExitCode: ExitOK}
	if owner, rerr := claim.Read(run.PrivateDir()); rerr == nil {
		out.Owner = &owner
	}
	if res, ok, rerr := readResultIfPresent(run); rerr == nil && ok {
		out.Result = &res
		out.ExitCode = ExitFromResult(res)
	}
	return out, nil
}

func reconcileAppendTerminal(run state.RunDir, runID string, res execution.Result) (ReconcileOutcome, error) {
	term, err := HistoryTerminal(run)
	if err != nil {
		return ReconcileOutcome{}, err
	}
	if term {
		return ReconcileOutcome{NoOp: true, Result: &res, ExitCode: ExitFromResult(res)}, nil
	}
	j, err := journal.OpenAppend(run.EventsPath(), runID)
	if err != nil {
		return ReconcileOutcome{}, err
	}
	defer j.Close()
	details := map[string]any{
		"status":         res.Status,
		"terminal_phase": res.TerminalPhase,
		"reason_code":    res.ReasonCode,
	}
	if _, err := j.Append(execution.PhaseTerminal, execution.KindRunTerminal, details); err != nil {
		return ReconcileOutcome{}, err
	}
	return ReconcileOutcome{Mutated: true, Result: &res, ExitCode: ExitFromResult(res)}, nil
}

func reconcileControllerLost(run state.RunDir, runID string, cleanup local.CleanupResult) (ReconcileOutcome, error) {
	reqBytes, err := os.ReadFile(run.RequestPath())
	if err != nil {
		return ReconcileOutcome{}, fmt.Errorf("controller: read request for reconcile: %w", err)
	}
	req, err := execution.DecodeRequest(reqBytes)
	if err != nil {
		return ReconcileOutcome{}, err
	}
	workBytes, err := os.ReadFile(run.WorkPath())
	workSHA := ""
	if err != nil {
		if !os.IsNotExist(err) {
			return ReconcileOutcome{}, fmt.Errorf("controller: read work for reconcile: %w", err)
		}
		workSHA = req.Work.SHA256
	} else {
		workSHA = sha256Hex(workBytes)
	}
	started := reconcileStartedAt(run)
	ended := time.Now().UTC()
	res := execution.Result{
		SchemaVersion: execution.SchemaVersion,
		RunID:         runID,
		RequestID:     req.RequestID,
		RequestSHA256: sha256Hex(reqBytes),
		WorkSHA256:    workSHA,
		Status:        execution.StatusFailed,
		TerminalPhase: execution.PhaseTerminal,
		ReasonCode:    execution.ReasonControllerLost,
		StartedAt:     started,
		EndedAt:       ended.Format(time.RFC3339Nano),
		Placement:     reconcilePlacement(run, req),
		Causes:        []execution.Cause{},
		Diagnostics:   []execution.Diagnostic{},
		Artifacts:     []execution.Artifact{},
	}
	if cleanup.Uncertain {
		res.Diagnostics = append(res.Diagnostics, execution.Diagnostic{
			Code:    execution.ReasonCleanupFailed,
			Message: "backend allocation liveness is uncertain after best-effort cleanup",
			Details: map[string]any{"allocation_id": cleanup.AllocationID},
		})
	}
	if err := execution.ValidateResult(res); err != nil {
		return ReconcileOutcome{}, fmt.Errorf("controller: invalid controller_lost result: %w", err)
	}

	j, err := openJournalForReconcile(run, runID)
	if err != nil {
		return ReconcileOutcome{}, err
	}
	defer j.Close()

	gate := &terminalGate{}
	committed, mutated, err := gate.commit(run, j, res)
	if err != nil {
		return ReconcileOutcome{}, err
	}
	return ReconcileOutcome{
		Mutated:  mutated,
		Result:   &committed,
		ExitCode: ExitFromResult(committed),
	}, nil
}

func openJournalForReconcile(run state.RunDir, runID string) (*journal.Writer, error) {
	if _, err := os.Stat(run.EventsPath()); os.IsNotExist(err) {
		return journal.Create(run.EventsPath(), runID)
	}
	return journal.OpenAppend(run.EventsPath(), runID)
}

func reconcileStartedAt(run state.RunDir) string {
	events, err := journal.ReadHistory(run.EventsPath())
	if err != nil || len(events) == 0 {
		return time.Now().UTC().Format(time.RFC3339Nano)
	}
	return events[0].Time
}

func reconcilePlacement(run state.RunDir, req execution.Request) execution.PlacementReceipt {
	alloc := "none"
	data, err := os.ReadFile(run.BackendPath())
	if err == nil {
		var a local.Allocation
		if json.Unmarshal(data, &a) == nil && a.PID > 0 {
			alloc = fmt.Sprintf("pid:%d", a.PID)
		}
	}
	return execution.PlacementReceipt{
		Backend:        req.Placement.Backend,
		Profile:        req.Placement.Profile,
		AllocationID:   alloc,
		StreamDelivery: execution.StreamDeliveryNone,
	}
}
