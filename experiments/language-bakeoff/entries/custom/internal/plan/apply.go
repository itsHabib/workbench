package plan

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/intent"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/kind"
)

// Outcome is what apply did for one declaration.
type Outcome struct {
	ID     string `json:"id"`
	Result string `json:"result"` // created, updated, unchanged, ran, skipped, failed, stale, not attempted
	Detail string `json:"detail,omitempty"`
}

// Report lists outcomes in the order apply reached them.
type Report struct {
	Outcomes []Outcome `json:"outcomes"`
}

// StaleError means the workspace no longer matches what the plan observed.
type StaleError struct{ Diffs []string }

func (e *StaleError) Error() string { return "stale plan: " + strings.Join(e.Diffs, "; ") }

// ConflictError means the plan contains a change wb will not make safely.
type ConflictError struct{ Changes []Change }

func (e *ConflictError) Error() string {
	var parts []string
	for _, ch := range e.Changes {
		parts = append(parts, ch.ID+": "+ch.Summary)
	}
	return "plan has conflicts: " + strings.Join(parts, "; ")
}

// EffectError means an effect failed partway through apply.
type EffectError struct {
	ID  string
	Err error
}

func (e *EffectError) Error() string { return e.ID + ": " + e.Err.Error() }
func (e *EffectError) Unwrap() error { return e.Err }

// Apply performs a plan. It first re-observes every fact the plan recorded
// and refuses, changing nothing, if any differs or the plan has conflicts.
// It then settles declarations in dependency order, re-deciding each piece
// of work just before it would run, and stops at the first failure. Nothing
// is rolled back: completed effects stay and are recorded, so a retry skips
// them.
func Apply(ctx context.Context, p *Plan, reg *kind.Registry, ws *foundation.Workspace) (*Report, error) {
	if p.Format != Format || len(p.Changes) != len(p.Decls) {
		return nil, fmt.Errorf("not a format-%d plan with one change per declaration", Format)
	}
	now, err := Make(&intent.Intent{Source: p.Source, Decls: p.Decls}, reg, ws)
	if err != nil {
		return nil, err
	}
	if diffs := staleDiffs(p.Checks, now.Checks); len(diffs) > 0 {
		return nil, &StaleError{Diffs: diffs}
	}
	if cs := conflicts(p); len(cs) > 0 {
		return nil, &ConflictError{Changes: cs}
	}
	j, err := ws.Journal()
	if err != nil {
		return nil, err
	}
	x := &executor{state: newState(reg, ws, j), journal: j, planned: p}
	rep := &Report{}
	for i, d := range p.Decls {
		out, err := x.settle(ctx, d)
		rep.Outcomes = append(rep.Outcomes, out)
		if err != nil {
			rep.Outcomes = append(rep.Outcomes, notAttempted(p.Decls[i+1:])...)
			return rep, err
		}
	}
	return rep, nil
}

func conflicts(p *Plan) []Change {
	var out []Change
	for _, ch := range p.Changes {
		if ch.Action == string(kind.Conflict) {
			out = append(out, ch)
		}
	}
	return out
}

func notAttempted(rest []intent.Decl) []Outcome {
	var out []Outcome
	for _, d := range rest {
		out = append(out, Outcome{ID: d.ID, Result: "not attempted"})
	}
	return out
}

// staleDiffs lists every fact that differs between plan time and now, in
// both directions: a fact the plan never recorded is as stale as one that
// changed, so a plan cannot drop its own checks.
func staleDiffs(was, now []Check) []string {
	cur, old := index(now), index(was)
	var diffs []string
	for _, c := range was {
		v, ok := cur[[2]string{c.ID, c.Subject}]
		if ok && v == c.Value {
			continue
		}
		diffs = append(diffs, describeDiff(c, v))
	}
	for _, c := range now {
		if _, ok := old[[2]string{c.ID, c.Subject}]; !ok {
			diffs = append(diffs, sprintf("%s: the plan never observed %s", c.ID, c.Subject))
		}
	}
	return diffs
}

func index(checks []Check) map[[2]string]string {
	m := map[[2]string]string{}
	for _, c := range checks {
		m[[2]string{c.ID, c.Subject}] = c.Value
	}
	return m
}

func describeDiff(c Check, now string) string {
	if c.Subject == foundation.JournalPath {
		return sprintf("%s: the journal has new records since the plan (last was %s, now %s)", c.ID, c.Value, now)
	}
	if now == "" {
		return sprintf("%s: %s is no longer observed", c.ID, c.Subject)
	}
	return sprintf("%s: %s was %s at plan time, now %s", c.ID, c.Subject, Short(c.Value), Short(now))
}

type executor struct {
	*state
	journal *foundation.Journal
	planned *Plan
}

func (x *executor) settle(ctx context.Context, d intent.Decl) (Outcome, error) {
	pr, err := x.propose(d)
	if err != nil {
		return Outcome{ID: d.ID, Result: "failed", Detail: err.Error()}, &EffectError{ID: d.ID, Err: err}
	}
	switch pr.change.Action {
	case string(kind.OK):
		return Outcome{ID: d.ID, Result: "unchanged"}, nil
	case Skip:
		return Outcome{ID: d.ID, Result: "skipped", Detail: pr.change.Reasons[0]}, nil
	case string(kind.Create), string(kind.Update):
		return x.converge(d, pr)
	case RunNow:
		return x.run(ctx, d, pr)
	}
	diff := sprintf("%s: now proposes %s: %s", d.ID, pr.change.Action, pr.change.Summary)
	return Outcome{ID: d.ID, Result: "stale", Detail: diff}, &StaleError{Diffs: []string{diff}}
}

// converge applies one resource change, after checking that nothing moved
// the resource since the plan observed it.
func (x *executor) converge(d intent.Decl, pr proposal) (Outcome, error) {
	if diffs := staleDiffs(x.planned.checksFor(d.ID), pr.checks); len(diffs) > 0 {
		return Outcome{ID: d.ID, Result: "stale", Detail: diffs[0]}, &StaleError{Diffs: diffs}
	}
	res, _ := x.reg.Resource(d.Kind)
	after, err := res.Apply(x.ws, d)
	if err != nil {
		return Outcome{ID: d.ID, Result: "failed", Detail: err.Error()}, &EffectError{ID: d.ID, Err: err}
	}
	rec, err := x.journal.Append(foundation.Record{
		Op: foundation.OpConverge, ID: d.ID, Kind: d.Kind,
		Action: pr.change.Action, Subject: after.Subject, After: after.Digest,
	})
	if err != nil {
		return Outcome{ID: d.ID, Result: "failed", Detail: "effect done, journal write failed: " + err.Error()}, err
	}
	result := map[string]string{string(kind.Create): "created", string(kind.Update): "updated"}[pr.change.Action]
	return Outcome{ID: d.ID, Result: result, Detail: sprintf("%s  (journal #%d)", after.Subject, rec.Seq)}, nil
}

// run performs one piece of work. The start record is written before the
// effect, so an interrupted run is visible to the next caller.
func (x *executor) run(ctx context.Context, d intent.Decl, pr proposal) (Outcome, error) {
	w, _ := x.reg.Work(d.Kind)
	start, err := x.journal.Append(foundation.Record{
		Op: foundation.OpStart, ID: d.ID, Kind: d.Kind, Def: d.Digest(), Inputs: pr.inputs,
	})
	if err != nil {
		return Outcome{ID: d.ID, Result: "failed", Detail: "journal write failed: " + err.Error()}, err
	}
	out, runErr := w.Run(ctx, x.ws, d)
	if runErr != nil {
		return x.failed(d, start, runErr)
	}
	fin, err := x.journal.Append(foundation.Record{
		Op: foundation.OpFinish, ID: d.ID, Kind: d.Kind, Attempt: start.Seq, Subject: out.Subject, After: out.Digest,
	})
	if err != nil {
		return Outcome{ID: d.ID, Result: "failed", Detail: "ran, journal write failed: " + err.Error()}, err
	}
	x.settled(d.ID, out.Digest)
	return Outcome{ID: d.ID, Result: "ran", Detail: sprintf("%s %s  (journal #%d-#%d)", out.Subject, Short(out.Digest), start.Seq, fin.Seq)}, nil
}

func (x *executor) failed(d intent.Decl, start foundation.Record, runErr error) (Outcome, error) {
	fail, err := x.journal.Append(foundation.Record{
		Op: foundation.OpFail, ID: d.ID, Kind: d.Kind, Attempt: start.Seq, Detail: runErr.Error(),
	})
	if err != nil {
		return Outcome{ID: d.ID, Result: "failed", Detail: runErr.Error()}, errors.Join(&EffectError{ID: d.ID, Err: runErr}, err)
	}
	return Outcome{ID: d.ID, Result: "failed", Detail: sprintf("%v  (journal #%d-#%d)", runErr, start.Seq, fail.Seq)}, &EffectError{ID: d.ID, Err: runErr}
}

func (p *Plan) checksFor(id string) []Check {
	var out []Check
	for _, c := range p.Checks {
		if c.ID == id {
			out = append(out, c)
		}
	}
	return out
}
