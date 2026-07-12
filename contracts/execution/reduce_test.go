package execution

import "testing"

func ev(seq int64, phase, kind string) RunEvent {
	return RunEvent{
		SchemaVersion: SchemaVersion,
		RunID:         "run_1",
		Seq:           seq,
		Time:          "2026-07-10T16:40:13.585Z",
		Phase:         phase,
		Kind:          kind,
	}
}

// successHistory is the Flow A event-stream shape: every phase in order,
// ending in run_terminal. Seq 2 carries an additive kind the reducer has
// never heard of — kinds are open vocabulary and ordering laws must not
// depend on them.
func successHistory() []RunEvent {
	return []RunEvent{
		ev(1, PhaseAdmission, KindRunAccepted),
		ev(2, PhasePreparation, "inputs_materialized"),
		ev(3, PhaseStartup, KindPlacementAllocated),
		ev(4, PhaseStartup, KindWorkloadReady),
		ev(5, PhaseWorkload, KindWorkloadStarted),
		ev(6, PhaseWorkload, KindWorkloadExited),
		ev(7, PhaseCollection, KindArtifactCollected),
		ev(8, PhaseCleanup, KindCleanupCompleted),
		ev(9, PhaseTerminal, KindRunTerminal),
	}
}

func TestReduce_SuccessFlow(t *testing.T) {
	state, err := Reduce(successHistory())
	if err != nil {
		t.Fatalf("Flow A history must reduce cleanly: %v", err)
	}
	if !state.Terminal || state.Phase != PhaseTerminal || state.LastSeq != 9 {
		t.Fatalf("wrong reduced view: %+v", state)
	}
	if state.TerminalEvent == nil || state.TerminalEvent.Kind != KindRunTerminal {
		t.Fatalf("terminal event not captured: %+v", state.TerminalEvent)
	}
}

func TestReduce_EmptyHistory(t *testing.T) {
	state, err := Reduce(nil)
	if err != nil {
		t.Fatalf("an empty history is a run that has recorded nothing yet: %v", err)
	}
	if state != (HistoryState{}) {
		t.Fatalf("empty history must reduce to the zero state, got %+v", state)
	}
}

// TestReduce_BackpressureFlow is the Flow B shape: a short history — the run
// was accepted, then placement backpressure terminated it at startup. The
// event stream carries no backpressure kind; the truncation plus run_terminal
// is the entire signal, and the failure detail lives in the result.
func TestReduce_BackpressureFlow(t *testing.T) {
	state, err := Reduce([]RunEvent{
		ev(1, PhaseAdmission, KindRunAccepted),
		ev(2, PhaseTerminal, KindRunTerminal),
	})
	if err != nil {
		t.Fatalf("Flow B history must reduce cleanly: %v", err)
	}
	if !state.Terminal || state.LastSeq != 2 {
		t.Fatalf("wrong reduced view: %+v", state)
	}
}

// TestReduce_TruncatedThenTerminalFlows are the timeout / cancel /
// controller-loss shapes (Flows C, D, F): a phase prefix truncates wherever
// the interruption landed and a run_terminal is appended — by the controller
// or by reconcile. At event level these flows are indistinguishable by
// construction; the result carries the status.
func TestReduce_TruncatedThenTerminalFlows(t *testing.T) {
	full := successHistory()
	for _, cut := range []int{1, 4, 6, 8} {
		prefix := append([]RunEvent{}, full[:cut]...)
		history := append(prefix, ev(int64(cut+1), PhaseTerminal, KindRunTerminal))
		state, err := Reduce(history)
		if err != nil {
			t.Fatalf("truncated-at-%d history must reduce cleanly: %v", cut, err)
		}
		if !state.Terminal || state.LastSeq != int64(cut+1) {
			t.Fatalf("truncated-at-%d: wrong reduced view: %+v", cut, state)
		}
	}
}

func TestReduce_OpenHistory(t *testing.T) {
	state, err := Reduce(successHistory()[:5])
	if err != nil {
		t.Fatalf("a history without a terminal event is open, not invalid: %v", err)
	}
	if state.Terminal || state.TerminalEvent != nil {
		t.Fatalf("open history must not report terminal: %+v", state)
	}
	if state.Phase != PhaseWorkload || state.LastSeq != 5 {
		t.Fatalf("wrong reduced view: %+v", state)
	}
}

func TestReduce_RejectsSeqGap(t *testing.T) {
	h := successHistory()
	h = append(h[:2], h[3:]...) // drop seq 3, keep later seqs
	if _, err := Reduce(h); err == nil {
		t.Fatal("a seq gap must reject")
	}
}

func TestReduce_RejectsDuplicateSeq(t *testing.T) {
	h := successHistory()
	h[3].Seq = h[2].Seq
	if _, err := Reduce(h); err == nil {
		t.Fatal("a duplicate seq must reject")
	}
}

func TestReduce_RejectsSeqNotFromOne(t *testing.T) {
	if _, err := Reduce(successHistory()[1:]); err == nil {
		t.Fatal("a history not starting at seq 1 must reject")
	}
}

func TestReduce_RejectsPhaseRegression(t *testing.T) {
	h := successHistory()
	h[5].Phase = PhaseStartup // workload -> startup
	if _, err := Reduce(h); err == nil {
		t.Fatal("a phase regression must reject")
	}
}

func TestReduce_RejectsUnknownPhase(t *testing.T) {
	h := successHistory()
	h[2].Phase = "warmup"
	if _, err := Reduce(h); err == nil {
		t.Fatal("an unknown phase must reject")
	}
}

func TestReduce_RejectsMixedRunIDs(t *testing.T) {
	h := successHistory()
	h[4].RunID = "run_2"
	if _, err := Reduce(h); err == nil {
		t.Fatal("mixed run ids must reject")
	}
}

func TestReduce_RejectsEventAfterTerminal(t *testing.T) {
	h := append(successHistory(), ev(10, PhaseTerminal, "late_echo"))
	if _, err := Reduce(h); err == nil {
		t.Fatal("an event after run_terminal must reject")
	}
}

func TestReduce_RejectsTerminalOutsideTerminalPhase(t *testing.T) {
	h := []RunEvent{
		ev(1, PhaseAdmission, KindRunAccepted),
		ev(2, PhaseCleanup, KindRunTerminal),
	}
	if _, err := Reduce(h); err == nil {
		t.Fatal("run_terminal outside the terminal phase must reject")
	}
}

// TestReduce_GeneratedSeqShuffles swaps every adjacent event pair of the
// valid history; each swap breaks seq contiguity and must reject.
func TestReduce_GeneratedSeqShuffles(t *testing.T) {
	n := len(successHistory())
	for i := 0; i < n-1; i++ {
		h := successHistory()
		h[i], h[i+1] = h[i+1], h[i]
		if _, err := Reduce(h); err == nil {
			t.Errorf("swap at %d must reject", i)
		}
	}
}

// TestReduce_GeneratedPhaseRegressions lowers each event's phase below its
// predecessor's; every such mutation must reject.
func TestReduce_GeneratedPhaseRegressions(t *testing.T) {
	phasesAsc := []string{PhaseAdmission, PhasePreparation, PhaseStartup,
		PhaseWorkload, PhaseCollection, PhaseCleanup, PhaseTerminal}
	base := successHistory()
	for i := 1; i < len(base); i++ {
		prevRank := phaseRank[base[i-1].Phase]
		if prevRank == 0 {
			continue
		}
		h := successHistory()
		h[i].Phase = phasesAsc[prevRank-1]
		if _, err := Reduce(h); err == nil {
			t.Errorf("regressing event %d to %q must reject", i, h[i].Phase)
		}
	}
}

// TestReduce_GeneratedDoubleTerminals promotes each non-final event to
// run_terminal; every resulting history carries two terminals and must
// reject — via the after-terminal law or, for an early promotion, the
// monotonicity law. Rejection is what matters.
func TestReduce_GeneratedDoubleTerminals(t *testing.T) {
	n := len(successHistory())
	for i := 0; i < n-1; i++ {
		h := successHistory()
		h[i].Kind = KindRunTerminal
		h[i].Phase = PhaseTerminal
		if _, err := Reduce(h); err == nil {
			t.Errorf("double terminal at %d must reject", i)
		}
	}
}
