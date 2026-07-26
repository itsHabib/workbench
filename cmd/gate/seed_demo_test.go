package main

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/gate/internal/capability"
	"github.com/itsHabib/workbench/cmd/gate/internal/state"
	"github.com/itsHabib/workbench/cmd/gate/internal/verify"
)

// TestSeedDemoState is a reproducible seeding harness for the Escalation-plane
// EVIDENCE transcript, not an assertion — it is a no-op unless GATE_DEMO_STATE
// and GATE_DEMO_KEY point at directories to seed. It drives gate's OWN act()
// park path — the same code a live gate run reaches — so the on-disk log and
// keyed anchor are byte-identical to what a real gate would write; the real
// gate/escalate binaries then run the resolve→stamp→audit thread against it. A
// live PR evidence-gather is not available offline, so the park (and only the
// park) is seeded here; everything downstream is the real binary end to end.
//
// Run: GATE_DEMO_STATE=<dir>/state GATE_DEMO_KEY=<dir>/keys go test ./cmd/gate
// -run TestSeedDemoState -count=1 -v
func TestSeedDemoState(t *testing.T) {
	stateDir := os.Getenv("GATE_DEMO_STATE")
	keyDir := os.Getenv("GATE_DEMO_KEY")
	if stateDir == "" || keyDir == "" {
		t.Skip("set GATE_DEMO_STATE and GATE_DEMO_KEY to seed the demo state")
	}
	e, err := newEnv(stateDir, "triage-floor", keyDir)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := capability.Mint(e.st, e.keyPath, "itsHabib/ship", "merge", "T2", 0, "operator", time.Hour, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	run := state.NewRunID()
	subject := verify.Subject{Repo: "itsHabib/ship", Number: 126, HeadSHA: "abc123"}
	question := "the reviewer panel disagrees on whether the retry loop can wedge"
	// The verifier verdicts a real run records before it reduces: a code floor
	// that passes (so readiness can be verified), and a review that escalates —
	// the disagreement that parks the run. runVerdicts recovers these + the
	// subject at resolve time, and the operator's pass overrides the escalate.
	floor := verify.Verdict{Subject: subject, Source: "triage-floor", Producer: verify.Producer{Class: verify.ClassCode}, Decision: verify.DecisionPass, Tier: "T0", Confidence: 1.0, Why: "floor checks pass"}
	if _, err := verify.Record(e.st, run, nil, floor); err != nil {
		t.Fatal(err)
	}
	review := verify.Verdict{Subject: subject, Source: "review-consolidation", Producer: verify.Producer{Class: verify.ClassLocal}, Decision: verify.DecisionEscalate, Tier: "T0", Confidence: 1.0, Why: question}
	if _, err := verify.Record(e.st, run, nil, review); err != nil {
		t.Fatal(err)
	}
	rv := verify.Verdict{Subject: subject, Source: "reducer", Producer: verify.Producer{Class: verify.ClassCode}, Decision: verify.DecisionEscalate, Tier: "T0", Confidence: 1.0, Why: question}
	rvID := recordReduced(t, e, run, rv)
	_, code, err := act(e, run, grant.ID, rv, rvID, gateResult{}, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	esc := firstOfKind(t, e, run, state.KindEscalation)
	fmt.Printf("DEMO_SEEDED grant=%s run=%s escalation=%s code=%d\n", grant.ID, run, esc.ID, code)
}
