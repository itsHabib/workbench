package execution

import "fmt"

// phaseRank orders the canonical phases. The reducer's monotonicity law and
// the terminal-combination law both key on membership and order.
var phaseRank = map[string]int{
	PhaseAdmission:   0,
	PhasePreparation: 1,
	PhaseStartup:     2,
	PhaseWorkload:    3,
	PhaseCollection:  4,
	PhaseCleanup:     5,
	PhaseTerminal:    6,
}

// HistoryState is the derived, read-only view Reduce computes from a run's
// canonical event history.
type HistoryState struct {
	Phase         string
	LastSeq       int64
	Terminal      bool
	TerminalEvent *RunEvent
}

// Reduce validates an existing event history against the contract's
// well-formedness laws — one run id, seq contiguous from 1, monotone phase
// order, at most one run_terminal and it is final — and returns the reduced
// view. It validates history; it never decides a transition, routes a
// backend, or drives a lifecycle: those are cmd/runway policy. An empty
// history is a run that has recorded nothing yet and reduces to the zero
// HistoryState.
func Reduce(events []RunEvent) (HistoryState, error) {
	var state HistoryState
	for i := range events {
		if err := admissible(state, events[i], i, events[0].RunID); err != nil {
			return HistoryState{}, err
		}
		state = advance(state, events[i])
	}
	return state, nil
}

func admissible(state HistoryState, e RunEvent, i int, runID string) error {
	if state.Terminal {
		return fmt.Errorf("execution: seq %d after run_terminal; the terminal event is final", e.Seq)
	}
	if e.RunID != runID {
		return fmt.Errorf("execution: seq %d run_id %q differs from the history's %q", e.Seq, e.RunID, runID)
	}
	if e.Seq != int64(i+1) {
		return fmt.Errorf("execution: seq %d at position %d; a history is contiguous from 1", e.Seq, i)
	}
	rank, ok := phaseRank[e.Phase]
	if !ok {
		return fmt.Errorf("execution: seq %d phase %q is not a canonical phase", e.Seq, e.Phase)
	}
	if state.Phase != "" && rank < phaseRank[state.Phase] {
		return fmt.Errorf("execution: seq %d phase %q regresses from %q; phase order is monotone", e.Seq, e.Phase, state.Phase)
	}
	if e.Kind == KindRunTerminal && e.Phase != PhaseTerminal {
		return fmt.Errorf("execution: seq %d run_terminal must carry phase %q, got %q", e.Seq, PhaseTerminal, e.Phase)
	}
	return nil
}

func advance(state HistoryState, e RunEvent) HistoryState {
	state.Phase = e.Phase
	state.LastSeq = e.Seq
	if e.Kind == KindRunTerminal {
		state.Terminal = true
		state.TerminalEvent = &e
	}
	return state
}
