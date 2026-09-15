// Package apply executes a plan's steps through adapters and journals every
// effect attempt. A failed step blocks its dependents; unrelated steps still
// run. Nothing is rolled back and nothing is assumed to be exactly-once.
package apply

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/evidence"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/intent"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/plan"
)

// Outcome is what happened to one step.
type Outcome string

// The outcomes.
const (
	Unchanged Outcome = "unchanged"
	Created   Outcome = "created"
	Updated   Outcome = "updated"
	Ran       Outcome = "ran"
	Failed    Outcome = "FAILED"
	Skipped   Outcome = "skipped"
)

// Result reports one step.
type Result struct {
	Address string
	Outcome Outcome
	Note    string
}

// Options are fault-injection hooks for demos and tests.
type Options struct {
	// FailBefore names a block whose effect fails before it starts.
	FailBefore string
	// AfterEffect runs after an effect completes and before its result is
	// journaled; the CLI uses it to simulate a crash in that window.
	AfterEffect func(address string)
}

// Apply runs p's steps in order. The error is reserved for failures that
// make further progress unsafe (the journal cannot be written, or the
// context is cancelled); step failures are reported in the results.
func Apply(ctx context.Context, p *plan.Plan, in *intent.Intent, env plan.Env, opts Options) ([]Result, error) {
	x := &executor{in: in, env: env, opts: opts, run: env.Journal.NextRun(), done: map[string]Outcome{}}
	var results []Result
	for _, s := range p.Steps {
		if err := ctx.Err(); err != nil {
			return results, fmt.Errorf("interrupted before %s: %w", s.Address, err)
		}
		r, err := x.step(ctx, s, in.Node(s.Address))
		if err != nil {
			return results, err
		}
		x.done[s.Address] = r.Outcome
		results = append(results, r)
	}
	return results, nil
}

type executor struct {
	in   *intent.Intent
	env  plan.Env
	opts Options
	run  int
	done map[string]Outcome
}

func (x *executor) step(ctx context.Context, s plan.Step, n *intent.Node) (Result, error) {
	if n == nil {
		return Result{}, fmt.Errorf("%s: step has no compiled node", s.Address)
	}
	if b := x.blocker(s.Deps); b != "" {
		return Result{Address: s.Address, Outcome: Skipped, Note: "upstream " + b + " did not complete"}, nil
	}
	if s.Action == plan.Keep {
		return Result{Address: s.Address, Outcome: Unchanged}, nil
	}
	ad, _ := x.env.Registry.Lookup(n.Type)
	if ra, ok := ad.(adapter.ResourceAdapter); ok && n.Kind == adapter.Resource {
		return x.resource(s, n, ra)
	}
	ta, ok := ad.(adapter.TaskAdapter)
	if !ok {
		return Result{}, fmt.Errorf("%s: adapter %q cannot run a task", s.Address, n.Type)
	}
	return x.task(ctx, s, n, ta)
}

func (x *executor) blocker(deps []string) string {
	for _, d := range deps {
		if o := x.done[d]; o == Failed || o == Skipped {
			return d
		}
	}
	return ""
}

func (x *executor) resource(s plan.Step, n *intent.Node, ra adapter.ResourceAdapter) (Result, error) {
	if err := x.record(s, evidence.Entry{Event: evidence.Started}); err != nil {
		return Result{}, err
	}
	got, err := x.converge(s, n, ra)
	if err != nil {
		return x.fail(s, err)
	}
	if err := x.record(s, evidence.Entry{Event: evidence.Succeeded, Fact: got.Fingerprint}); err != nil {
		return Result{}, err
	}
	outcome := Updated
	if s.Action == plan.Create {
		outcome = Created
	}
	note := got.Summary
	if len(n.Owns) > 0 {
		note = strings.Join(n.Owns, ", ") + ": " + got.Summary
	}
	return Result{Address: s.Address, Outcome: outcome, Note: note}, nil
}

// converge re-checks the plan's precondition immediately before the effect,
// so a change made after planning fails the step instead of being silently
// overwritten, then verifies the result by observing it.
func (x *executor) converge(s plan.Step, n *intent.Node, ra adapter.ResourceAdapter) (adapter.Fact, error) {
	if err := x.injected(s.Address); err != nil {
		return adapter.Fact{}, err
	}
	have, err := ra.Observe(x.env.Workspace, n.Attrs)
	if err != nil {
		return adapter.Fact{}, err
	}
	if planned := s.Observed[plan.StateKey]; have.Fingerprint != planned {
		return adapter.Fact{}, fmt.Errorf("changed since plan (%s at plan time, now %s); re-plan", plan.Short(planned), plan.Short(have.Fingerprint))
	}
	if err := ra.Apply(x.env.Workspace, n.Attrs); err != nil {
		return adapter.Fact{}, err
	}
	x.afterEffect(s.Address)
	got, err := ra.Observe(x.env.Workspace, n.Attrs)
	if err != nil {
		return adapter.Fact{}, err
	}
	if got.Fingerprint != s.Want {
		return adapter.Fact{}, fmt.Errorf("observed %s after apply, want %s", plan.Short(got.Fingerprint), plan.Short(s.Want))
	}
	return got, nil
}

func (x *executor) task(ctx context.Context, s plan.Step, n *intent.Node, ta adapter.TaskAdapter) (Result, error) {
	reasons, err := plan.StaleNow(n, x.in, x.env)
	if err != nil {
		return x.fail(s, err)
	}
	if len(reasons) == 0 {
		return Result{Address: s.Address, Outcome: Unchanged, Note: "fresh once upstream applied: config, upstream, inputs and outputs match its receipt; not re-run"}, nil
	}
	if err := x.record(s, evidence.Entry{Event: evidence.Started, Config: n.Attrs}); err != nil {
		return Result{}, err
	}
	receipt, err := x.runTask(ctx, s, n, ta)
	if err != nil {
		return x.fail(s, err)
	}
	if err := x.record(s, receipt); err != nil {
		return Result{}, err
	}
	return Result{Address: s.Address, Outcome: Ran, Note: describe(receipt.Outputs)}, nil
}

// runTask runs the task and builds its receipt: the configuration, the
// upstream state and input digests it ran against, and the output digests
// it produced.
func (x *executor) runTask(ctx context.Context, s plan.Step, n *intent.Node, ta adapter.TaskAdapter) (evidence.Entry, error) {
	ws := x.env.Workspace
	if err := x.injected(s.Address); err != nil {
		return evidence.Entry{}, err
	}
	upstream, err := plan.Upstream(n, x.in, x.env)
	if err != nil {
		return evidence.Entry{}, err
	}
	before, err := plan.Digests(ws, n.Reads)
	if err != nil {
		return evidence.Entry{}, err
	}
	if err := ta.Run(ctx, ws, n.Attrs); err != nil {
		return evidence.Entry{}, err
	}
	x.afterEffect(s.Address)
	after, err := plan.Digests(ws, n.Reads)
	if err != nil {
		return evidence.Entry{}, err
	}
	if !maps.Equal(before, after) {
		return evidence.Entry{}, errors.New("an input changed while the task ran; its output is untrustworthy and it will re-run")
	}
	outputs, err := plan.Digests(ws, n.Owns)
	if err != nil {
		return evidence.Entry{}, err
	}
	if missing := absent(outputs); missing != "" {
		return evidence.Entry{}, fmt.Errorf("the task finished without writing its declared output %s", missing)
	}
	return evidence.Entry{Event: evidence.Succeeded, Config: n.Attrs, Inputs: before, Outputs: outputs, Upstream: upstream}, nil
}

func absent(digests map[string]string) string {
	for p, d := range digests {
		if d == adapter.Absent {
			return p
		}
	}
	return ""
}

func (x *executor) record(s plan.Step, e evidence.Entry) error {
	e.Run = x.run
	e.Address = s.Address
	e.Action = string(s.Action)
	return x.env.Journal.Append(e)
}

func (x *executor) fail(s plan.Step, cause error) (Result, error) {
	if err := x.record(s, evidence.Entry{Event: evidence.Failed, Error: cause.Error()}); err != nil {
		return Result{}, err
	}
	return Result{Address: s.Address, Outcome: Failed, Note: cause.Error()}, nil
}

func (x *executor) injected(address string) error {
	if x.opts.FailBefore == address {
		return errors.New("injected fault before effect")
	}
	return nil
}

func (x *executor) afterEffect(address string) {
	if x.opts.AfterEffect != nil {
		x.opts.AfterEffect(address)
	}
}

func describe(digests map[string]string) string {
	parts := make([]string, 0, len(digests))
	for p, d := range digests {
		parts = append(parts, p+" "+plan.Short(d))
	}
	sort.Strings(parts)
	return "wrote " + strings.Join(parts, ", ")
}
