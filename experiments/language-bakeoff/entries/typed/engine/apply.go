package engine

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/wb"
)

// StaleError means a saved plan no longer matches the workspace.
type StaleError struct{ Drift []string }

func (e *StaleError) Error() string { return "plan is stale: " + strings.Join(e.Drift, "; ") }

// Status is the outcome of one step.
type Status string

// Step outcomes.
const (
	Unchanged Status = "unchanged"
	Done      Status = "done"
	Failed    Status = "failed"
	Skipped   Status = "skipped"
)

// Result is what happened to one step.
type Result struct {
	Step     Step
	Status   Status
	Detail   string
	Observed State // the node's object after the step
	Evidence int   // evidence record written for the step, if any
}

// Verify checks that a plan still describes the workspace: the evidence
// journal has not advanced and every node's object is as it was observed
// at plan time. Any difference means the plan's reasoning no longer
// holds, so no part of it may be applied.
func Verify(ws *foundation.Workspace, reg Registry, p Plan) error {
	if p.Version != PlanVersion {
		return fmt.Errorf("plan format version %d, want %d", p.Version, PlanVersion)
	}
	if err := checkPlanShape(reg, p); err != nil {
		return err
	}
	ev, err := ws.Evidence()
	if err != nil {
		return err
	}
	var drift []string
	if ev.Head() != p.EvidenceHead {
		drift = append(drift, fmt.Sprintf("evidence journal is at #%d but the plan saw #%d (another apply ran)",
			ev.Head(), p.EvidenceHead))
	}
	for _, s := range p.Steps {
		if d := verifyStep(ws, reg, p.Intent, s); d != "" {
			drift = append(drift, d)
		}
	}
	if len(drift) > 0 {
		return &StaleError{Drift: drift}
	}
	return nil
}

// checkPlanShape rejects a plan this program cannot apply, as opposed to
// one the workspace has outgrown: a corrupt or edited file, an invalid
// intent, steps that do not match the intent, or a kind with no adapter.
// The intent digest is unkeyed, so it catches accidents, not tampering.
func checkPlanShape(reg Registry, p Plan) error {
	if p.IntentDigest != p.Intent.Digest() {
		return errors.New("the plan's intent does not match its digest; the plan file was altered")
	}
	if err := p.Intent.Validate(); err != nil {
		return fmt.Errorf("the plan's intent is invalid: %w", err)
	}
	if len(p.Steps) != len(p.Intent.Nodes) {
		return errors.New("the plan's steps do not match its intent")
	}
	for i, n := range p.Intent.Nodes {
		if p.Steps[i].Address != n.Address || p.Steps[i].Class != n.Class {
			return fmt.Errorf("the plan's step %d is %s, but its intent has %s there", i, p.Steps[i].Address, n.Address)
		}
		if err := reg.check(n); err != nil {
			return fmt.Errorf("%s: %w", n.Address, err)
		}
	}
	return nil
}

func verifyStep(ws *foundation.Workspace, reg Registry, in wb.Intent, s Step) string {
	n, _ := in.Node(s.Address) // checkPlanShape matched every step to a node
	now, err := reg.observe(ws, n)
	if err != nil {
		return fmt.Sprintf("%s: can no longer be observed: %v", s.Address, err)
	}
	if same(now, s.Before) {
		return ""
	}
	return fmt.Sprintf("%s: %s was %s when planned, is now %s", s.Address, s.Owns, Describe(s.Before), Describe(now))
}

// Apply verifies the plan, then performs its steps in dependency order.
// A step whose dependency did not complete is skipped; unrelated steps
// still run. Completed effects are kept: there is no rollback. The
// returned error is non-nil only when nothing was attempted.
func Apply(ctx context.Context, ws *foundation.Workspace, reg Registry, p Plan) ([]Result, error) {
	if err := Verify(ws, reg, p); err != nil {
		return nil, err
	}
	a := applier{ws: ws, reg: reg, intent: p.Intent, broken: map[wb.Address]bool{}}
	results := make([]Result, 0, len(p.Steps))
	for _, s := range p.Steps {
		results = append(results, a.step(ctx, s))
	}
	return results, nil
}

type applier struct {
	ws     *foundation.Workspace
	reg    Registry
	intent wb.Intent
	broken map[wb.Address]bool // failed or skipped during this apply
}

func (a *applier) step(ctx context.Context, s Step) Result {
	if s.Op == None {
		return Result{Step: s, Status: Unchanged, Observed: s.Before}
	}
	n, _ := a.intent.Node(s.Address) // Verify established that it exists
	if d := a.brokenDep(n); d != "" {
		a.broken[s.Address] = true
		return Result{Step: s, Status: Skipped, Detail: fmt.Sprintf("dependency %s did not complete", d)}
	}
	res := Result{Step: s, Status: Done}
	var err error
	switch s.Class {
	case wb.Task:
		res.Observed, res.Evidence, err = a.run(ctx, n, s)
	default:
		res.Observed, res.Evidence, err = a.converge(n, s)
	}
	if err != nil {
		a.broken[s.Address] = true
		res.Status, res.Detail = Failed, err.Error()
	}
	return res
}

func (a *applier) brokenDep(n wb.Node) wb.Address {
	for _, d := range n.Deps {
		if a.broken[d] {
			return d
		}
	}
	return ""
}

// converge applies a resource change after re-checking, immediately
// before the effect, that the object is still as planned and that the
// adapter still proposes the saved change, then observes the result. Check and effect are not atomic: this narrows the window
// for a concurrent change, it does not close it.
func (a *applier) converge(n wb.Node, s Step) (State, int, error) {
	r, err := a.reg.resource(n)
	if err != nil {
		return State{}, 0, err
	}
	ch, err := r.Plan(a.ws, n)
	if err != nil {
		return State{}, 0, err
	}
	if !same(ch.Before, s.Before) {
		return State{}, 0, fmt.Errorf("%s changed during apply (planned against %s, found %s)",
			s.Owns, Describe(s.Before), Describe(ch.Before))
	}
	// The adapter must still propose what was reviewed: this refuses a
	// saved step whose op or desired state was edited by hand.
	if ch.Op != s.Op || !same(ch.After, s.After) {
		return State{}, 0, fmt.Errorf("the plan says %s to %s, but the node now plans %s to %s",
			s.Op, Describe(s.After), ch.Op, Describe(ch.After))
	}
	if err := r.Apply(a.ws, n, Change{Op: s.Op, Before: s.Before, After: s.After, Note: s.Note}); err != nil {
		return State{}, 0, err
	}
	now, err := a.reg.observe(a.ws, n)
	if err != nil {
		return State{}, 0, err
	}
	if !same(now, s.After) {
		return now, 0, fmt.Errorf("did not converge: observed %s, desired %s", Describe(now), Describe(s.After))
	}
	rec, err := a.ws.Record(foundation.Record{Event: foundation.EventApply, Subject: string(n.Address),
		Detail: string(s.Op), Output: now.Digest})
	return now, rec.Seq, err
}

// run executes a task once, bracketed by evidence: a start record before
// the effect and a finish record after it. A crash between the two leaves
// a start with no finish, which the next plan reports as interrupted and
// runs again. Replay is at-least-once, never exactly-once.
func (a *applier) run(ctx context.Context, n wb.Node, s Step) (State, int, error) {
	t, err := a.reg.task(n)
	if err != nil {
		return State{}, 0, err
	}
	inputs, err := a.inputs(n)
	if err != nil {
		return State{}, 0, err
	}
	if err := checkInputs(s.Inputs, inputs); err != nil {
		return State{}, 0, err
	}
	fp, in := fingerprint(n, inputs), stringKeys(inputs)
	start := foundation.Record{Event: foundation.EventStart, Subject: string(n.Address), Fingerprint: fp, Inputs: in}
	if _, err := a.ws.Record(start); err != nil {
		return State{}, 0, err
	}
	out, runErr := t.Run(ctx, a.ws, n)
	fin := foundation.Record{Event: foundation.EventFinish, Subject: string(n.Address), Fingerprint: fp, Inputs: in,
		Status: foundation.StatusOK, Output: out.Digest}
	if runErr != nil {
		fin.Status, fin.Output, fin.Detail = foundation.StatusFailed, "", runErr.Error()
	}
	rec, err := a.ws.Record(fin)
	if err != nil {
		return out, 0, errors.Join(runErr, err)
	}
	return out, rec.Seq, runErr
}

// inputs observes every dependency now, just before the task runs.
func (a *applier) inputs(n wb.Node) (map[wb.Address]string, error) {
	in := map[wb.Address]string{}
	for _, d := range n.Deps {
		dn, _ := a.intent.Node(d)
		st, err := a.reg.observe(a.ws, dn)
		if err != nil {
			return nil, fmt.Errorf("observe input %s: %w", d, err)
		}
		in[d] = st.Digest
	}
	return in, nil
}

// checkInputs refuses to run a task on inputs other than those the plan
// predicted, such as a file edited while the apply was in progress.
func checkInputs(want, got map[wb.Address]string) error {
	for _, d := range slices.Sorted(maps.Keys(want)) {
		if want[d] != KnownAfterApply && got[d] != want[d] {
			return fmt.Errorf("input %s is %s but the plan expected %s (changed during apply)",
				d, Short(got[d]), Short(want[d]))
		}
	}
	return nil
}

func stringKeys(m map[wb.Address]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[string(k)] = v
	}
	return out
}
