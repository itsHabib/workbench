package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/observe"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
	"github.com/itsHabib/workbench/contracts/escalation"
)

// TestGatePreflightRefusesOverCycleCap pins the pre-flight ceiling check: a
// `gate gate` whose cycle would exceed the grant's -max-cycles refuses BEFORE
// gathering evidence — exit 3, code grant_cycle_exceeded, a grant_needed
// record under the run — instead of sweeping the PR and parking. PATH is
// stripped so any path that reached evidence (gh) would hard-error, proving
// the refusal precedes it. The refusal burns no cycle, and the PR's existing
// content park stays in the inbox, labelled with its budget, and judgeable
// under the same grant — the state the Aug 2026 sweep lost by re-running gate
// until the park itself was over the ceiling.
func TestGatePreflightRefusesOverCycleCap(t *testing.T) {
	t.Setenv("PATH", "")
	e := testEnv(t)
	capped, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 2, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	subject := verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "abc"}
	parkRun := spendTwoCycles(t, e, capped.ID, subject)

	// Cycle 3 would exceed the ceiling of 2: refused pre-flight.
	res, code, err := runGate(e, "o/r", 7, capped.ID, false, "local", false)
	if err != nil {
		t.Fatalf("pre-flight refusal must be a clean result, not an error: %v", err)
	}
	if code != codeRefused || res.Outcome != "capability_refused" || res.Code != escalation.CodeCycleExceeded {
		t.Fatalf("over-cap gate: code %d outcome %q code %q, want refused/capability_refused/%s",
			code, res.Outcome, res.Code, escalation.CodeCycleExceeded)
	}
	if res.CyclesUsed != 2 || res.CyclesMax != 2 {
		t.Fatalf("refusal budget = %d/%d, want 2/2", res.CyclesUsed, res.CyclesMax)
	}
	if !strings.Contains(res.Why, "cycle 3 would exceed grant ceiling 2") {
		t.Fatalf("refusal why must name the cycle and ceiling, got %q", res.Why)
	}
	if res.Run == "" {
		t.Fatal("refusal must mint a run id so explain can reconstruct it")
	}
	assertCycleRefusalRecorded(t, e, res.Run, capped.ID)

	// The refusal consumed nothing: the count a later run sees is still 2.
	n, err := cycleCount(e.st, subject, state.NewRunID())
	if err != nil || n != 2 {
		t.Fatalf("cycle count after refusal = %d err %v, want 2 (refusal burns no cycle)", n, err)
	}
	assertParkSurvivesRefusal(t, e, parkRun)

	// And the park is still judgeable under the capped grant: a judgment
	// re-enters act on its run, whose own cycle is excluded from the count.
	judged := reducedVerdict(subject, verify.DecisionPass, "T0")
	res, code, err = act(e, parkRun, capped.ID, judged, recordReduced(t, e, parkRun, judged), gateResult{}, false, nil)
	if err != nil || code != codeMerge {
		t.Fatalf("the park must stay judgeable after a pre-flight refusal: code %d outcome %q err %v", code, res.Outcome, err)
	}
}

// spendTwoCycles records cycle 1 as a pass and cycle 2 as a content park for
// subject under grantID, asserting each result carries the budget it was
// checked against, and returns the parked run.
func spendTwoCycles(t *testing.T, e env, grantID string, subject verify.Subject) string {
	t.Helper()
	run1 := state.NewRunID()
	v1 := reducedVerdict(subject, verify.DecisionPass, "T0")
	res, code, err := act(e, run1, grantID, v1, recordReduced(t, e, run1, v1), gateResult{}, false, nil)
	if err != nil || code != codeMerge {
		t.Fatalf("cycle 1: code %d err %v", code, err)
	}
	if res.CyclesUsed != 0 || res.CyclesMax != 2 {
		t.Fatalf("cycle 1 budget = %d/%d, want 0/2", res.CyclesUsed, res.CyclesMax)
	}
	run2 := state.NewRunID()
	v2 := reducedVerdict(subject, verify.DecisionEscalate, "T0")
	res, code, err = act(e, run2, grantID, v2, recordReduced(t, e, run2, v2), gateResult{}, false, nil)
	if err != nil || code != codeParked {
		t.Fatalf("cycle 2: code %d err %v", code, err)
	}
	if res.CyclesUsed != 1 || res.CyclesMax != 2 {
		t.Fatalf("cycle 2 budget = %d/%d, want 1/2", res.CyclesUsed, res.CyclesMax)
	}
	return run2
}

// assertParkSurvivesRefusal checks the inbox after a pre-flight refusal: the
// content park is still the PR's newest terminal — the refusal must not
// supersede it — its row carries the budget in JSON and text, and the
// needs-grant surface stays quiet because the grant is live, just spent.
func assertParkSurvivesRefusal(t *testing.T, e env, parkRun string) {
	t.Helper()
	in := nextInbox(t, e)
	if len(in.Parked) != 1 || in.Parked[0].Run != parkRun {
		t.Fatalf("inbox must still show the cycle-2 park, got %+v", in.Parked)
	}
	if in.Parked[0].CyclesUsed != 2 || in.Parked[0].CyclesMax != 2 {
		t.Fatalf("parked row budget = %d/%d, want 2/2", in.Parked[0].CyclesUsed, in.Parked[0].CyclesMax)
	}
	if len(in.NeedsGrant) != 0 {
		t.Fatalf("a cycle refusal is not a lapsed grant, got needs_grant %+v", in.NeedsGrant)
	}
	var text bytes.Buffer
	if err := observe.NextText(&text, e.st, time.Now, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "o/r#7  "+parkRun+"  cycles 2/2") {
		t.Fatalf("next text must label the park with its budget:\n%s", text.String())
	}
}

// TestGatePreflightUnderCapProceedsToEvidence pins the other side: a run whose
// cycle fits the ceiling is not refused pre-flight. With gh absent the run
// hard-errors at evidence (exit 4) — reaching evidence at all is the proof —
// and the errored result still carries the budget it was checked against.
func TestGatePreflightUnderCapProceedsToEvidence(t *testing.T) {
	t.Setenv("PATH", "")
	e := testEnv(t)
	g, err := capability.Mint(e.st, e.keyPath, "o/r", "merge", "T1", 3, "test", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	subject := verify.Subject{Repo: "o/r", Number: 7, HeadSHA: "abc"}
	for i := 1; i <= 2; i++ {
		run := state.NewRunID()
		v := reducedVerdict(subject, verify.DecisionPass, "T0")
		if _, code, err := act(e, run, g.ID, v, recordReduced(t, e, run, v), gateResult{}, false, nil); err != nil || code != codeMerge {
			t.Fatalf("cycle %d: code %d err %v", i, code, err)
		}
	}
	res, code, err := runGate(e, "o/r", 7, g.ID, false, "local", false)
	if err == nil || code != codeError {
		t.Fatalf("cycle 3 of 3 must proceed to evidence (and hard-error there with gh absent): code %d outcome %q err %v",
			code, res.Outcome, err)
	}
	if res.CyclesUsed != 2 || res.CyclesMax != 3 {
		t.Fatalf("budget = %d/%d, want 2/3", res.CyclesUsed, res.CyclesMax)
	}
	if n := len(grantNeededArts(t, e)); n != 0 {
		t.Fatalf("an under-cap run must record no refusal, got %d grant_needed artifacts", n)
	}
}

// TestGateResultJSONCarriesCycleBudget pins the output contract: cycles_used
// and cycles_max are present on every result, zero included, so a driver can
// read the budget without special-casing paths that finalized before a grant.
func TestGateResultJSONCarriesCycleBudget(t *testing.T) {
	raw, err := json.Marshal(gateResult{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"cycles_used":0`, `"cycles_max":0`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("gateResult JSON missing %s: %s", want, raw)
		}
	}
}

// assertCycleRefusalRecorded checks the pre-flight refusal's durable trace: one
// grant_needed artifact under the run — never an action or escalation, which
// would read as a consumed cycle or supersede the PR's park — naming the
// reason, the grant, and the budget it was refused against.
func assertCycleRefusalRecorded(t *testing.T, e env, run, grantID string) {
	t.Helper()
	arts := mustRunArtifacts(t, e, run)
	if len(arts) != 1 || arts[0].Kind != state.KindGrantNeeded {
		t.Fatalf("refusal run must hold exactly one grant_needed artifact, got %+v", arts)
	}
	var body struct {
		Repo       string `json:"repo"`
		Number     int    `json:"number"`
		Reason     string `json:"reason"`
		Grant      string `json:"grant"`
		CyclesUsed int    `json:"cycles_used"`
		CyclesMax  int    `json:"cycles_max"`
	}
	if err := json.Unmarshal(arts[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Repo != "o/r" || body.Number != 7 || body.Reason != escalation.CodeCycleExceeded || body.Grant != grantID {
		t.Fatalf("refusal body = %+v", body)
	}
	if body.CyclesUsed != 2 || body.CyclesMax != 2 {
		t.Fatalf("refusal body budget = %d/%d, want 2/2", body.CyclesUsed, body.CyclesMax)
	}
	var explained bytes.Buffer
	if err := observe.Explain(&explained, e.st, run); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(explained.String(), "reason: "+escalation.CodeCycleExceeded) {
		t.Fatalf("explain must render the refusal's fields:\n%s", explained.String())
	}
}

// nextInbox reads the inbox projection the way the console does.
func nextInbox(t *testing.T, e env) observe.Inbox {
	t.Helper()
	var buf bytes.Buffer
	if err := observe.NextJSON(&buf, e.st, time.Now, ""); err != nil {
		t.Fatal(err)
	}
	var in observe.Inbox
	if err := json.Unmarshal(buf.Bytes(), &in); err != nil {
		t.Fatal(err)
	}
	return in
}
