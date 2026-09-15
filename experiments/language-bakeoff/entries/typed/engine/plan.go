package engine

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/typed/wb"
)

// PlanVersion is the saved-plan format version.
const PlanVersion = 2

// KnownAfterApply stands in for a digest that exists only once an earlier
// step of the same apply has run: the output of an upstream task.
const KnownAfterApply = "(known after apply)"

// Plan is a proposal to review before any effect: the intent, what was
// observed, and the steps that would converge the workspace. It is valid
// only against the workspace state it was computed from.
type Plan struct {
	Version      int       `json:"version"`
	Intent       wb.Intent `json:"intent"`
	IntentDigest string    `json:"intent_digest"`
	EvidenceHead int       `json:"evidence_head"`
	Steps        []Step    `json:"steps"`
	Warnings     []string  `json:"warnings,omitempty"`
}

// Step is the planned action for one node, in dependency order.
type Step struct {
	Address wb.Address `json:"address"`
	Class   wb.Class   `json:"class"`
	Op      Op         `json:"op"`
	Owns    string     `json:"owns"`
	Before  State      `json:"before"`            // observed at plan time
	After   State      `json:"after,omitzero"`    // resources: desired
	Note    string     `json:"note,omitempty"`    // adapter's explanation
	Reasons []string   `json:"reasons,omitempty"` // tasks: why it runs
	// Inputs are the dependency digests a task is expected to read, as
	// predicted at plan time, or KnownAfterApply.
	Inputs  map[wb.Address]string `json:"inputs,omitempty"`
	LastRun int                   `json:"last_run,omitempty"` // tasks: newest successful run
}

// Pending lists the steps that would change something.
func (p Plan) Pending() []Step {
	var out []Step
	for _, s := range p.Steps {
		if s.Op != None {
			out = append(out, s)
		}
	}
	return out
}

// Compute validates in, observes the workspace and plans every node. It
// performs no effects and writes nothing.
func Compute(ws *foundation.Workspace, reg Registry, in wb.Intent) (Plan, error) {
	if err := in.Validate(); err != nil {
		return Plan{}, fmt.Errorf("invalid intent: %w", err)
	}
	ev, err := ws.Evidence()
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Version: PlanVersion, Intent: in, IntentDigest: in.Digest(), EvidenceHead: ev.Head()}
	if ev.Unreadable > 0 {
		p.Warnings = append(p.Warnings,
			fmt.Sprintf("%d unreadable evidence line(s) ignored; work they recorded may run again", ev.Unreadable))
	}
	pl := planner{ws: ws, reg: reg, runs: history(ev.Records), planned: map[wb.Address]Step{}}
	for _, n := range in.Nodes {
		s, err := pl.step(n)
		if err != nil {
			return Plan{}, fmt.Errorf("%s: %w", n.Address, err)
		}
		pl.planned[n.Address] = s
		p.Steps = append(p.Steps, s)
	}
	return p, nil
}

type planner struct {
	ws      *foundation.Workspace
	reg     Registry
	runs    map[wb.Address]runs
	planned map[wb.Address]Step
}

func (pl *planner) step(n wb.Node) (Step, error) {
	if n.Class == wb.Task {
		return pl.task(n)
	}
	return pl.resource(n)
}

func (pl *planner) resource(n wb.Node) (Step, error) {
	r, err := pl.reg.resource(n)
	if err != nil {
		return Step{}, err
	}
	ch, err := r.Plan(pl.ws, n)
	if err != nil {
		return Step{}, err
	}
	if !slices.Contains([]Op{None, Create, Update, Replace}, ch.Op) {
		return Step{}, fmt.Errorf("adapter proposed invalid op %q", ch.Op)
	}
	return Step{Address: n.Address, Class: n.Class, Op: ch.Op, Owns: n.Owns,
		Before: ch.Before, After: ch.After, Note: ch.Note}, nil
}

func (pl *planner) task(n wb.Node) (Step, error) {
	t, err := pl.reg.task(n)
	if err != nil {
		return Step{}, err
	}
	out, err := t.Observe(pl.ws, n)
	if err != nil {
		return Step{}, err
	}
	s := Step{Address: n.Address, Class: n.Class, Op: None, Owns: n.Owns, Before: out,
		Inputs: map[wb.Address]string{}}
	for _, d := range n.Deps {
		s.Inputs[d] = pl.planned[d].yields()
	}
	h := pl.runs[n.Address]
	if h.ok != nil {
		s.LastRun = h.ok.Seq
	}
	s.Reasons = pl.staleness(n, s, h)
	if len(s.Reasons) > 0 {
		s.Op = Run
	}
	return s, nil
}

// yields is the digest dependents will read once this step has applied.
func (s Step) yields() string {
	if s.Class == wb.Resource {
		return s.After.Digest
	}
	if s.Op == Run {
		return KnownAfterApply
	}
	return s.Before.Digest
}

// staleness lists why a task must run; none means its evidence still
// vouches for its output. The replay rule: a task is current when its
// newest successful run had the same fingerprint (kind, spec and every
// input digest) and its output is still exactly what that run produced.
func (pl *planner) staleness(n wb.Node, s Step, h runs) []string {
	reasons := pl.upstream(n)
	if h.ok == nil {
		return append(reasons, neverSucceeded(h.latest))
	}
	reasons = append(reasons, inputChanges(n, s.Inputs, h.ok)...)
	reasons = append(reasons, outputChanges(s, h.ok)...)
	if len(reasons) > 0 && h.latest != h.ok {
		reasons = append(reasons, "latest attempt: "+describeAttempt(h.latest))
	}
	return reasons
}

// upstream reports dependencies whose planned change invalidates this
// task whatever the digests say: a replaced container deletes what is
// inside it, and a dependency that runs first has no output digest yet.
func (pl *planner) upstream(n wb.Node) []string {
	var r []string
	for _, d := range n.Deps {
		dep := pl.planned[d]
		if dep.Op == Replace {
			r = append(r, fmt.Sprintf("dependency %s will be replaced", d))
		}
		if dep.Class == wb.Task && dep.Op == Run {
			r = append(r, fmt.Sprintf("dependency %s runs first; its output is known only after apply", d))
		}
	}
	return r
}

func inputChanges(n wb.Node, inputs map[wb.Address]string, ok *foundation.Record) []string {
	fp, known := fingerprintOf(n, inputs)
	if known && fp == ok.Fingerprint {
		return nil
	}
	var r []string
	for _, d := range slices.Sorted(maps.Keys(inputs)) {
		now, was := inputs[d], ok.Inputs[string(d)]
		if now != KnownAfterApply && now != was {
			r = append(r, fmt.Sprintf("input %s changed since run #%d (%s -> %s)", d, ok.Seq, Short(was), Short(now)))
		}
	}
	if len(r) == 0 && known {
		r = append(r, fmt.Sprintf("definition changed since run #%d", ok.Seq))
	}
	return r
}

func outputChanges(s Step, ok *foundation.Record) []string {
	if !s.Before.Exists {
		return []string{fmt.Sprintf("output %s is missing", s.Owns)}
	}
	if s.Before.Digest != ok.Output {
		return []string{fmt.Sprintf("output %s is not what run #%d produced (changed outside the workflow); running again overwrites it", s.Owns, ok.Seq)}
	}
	return nil
}

func neverSucceeded(latest *foundation.Record) string {
	if latest == nil {
		return "never run"
	}
	return "no successful run yet; " + describeAttempt(latest)
}

func describeAttempt(r *foundation.Record) string {
	if r.Event == foundation.EventStart {
		return fmt.Sprintf("run #%d started but never recorded a result (interrupted)", r.Seq)
	}
	return fmt.Sprintf("run #%d failed: %s", r.Seq, r.Detail)
}

// fingerprintOf identifies one execution: kind, spec and the digest of
// every dependency. It is unknown while any input is known only after apply.
func fingerprintOf(n wb.Node, inputs map[wb.Address]string) (string, bool) {
	for _, v := range inputs {
		if v == KnownAfterApply {
			return "", false
		}
	}
	return fingerprint(n, inputs), true
}

func fingerprint(n wb.Node, inputs map[wb.Address]string) string {
	// The encoder compacts Spec, so a plan read back from an indented file
	// fingerprints the same as the in-memory intent it was saved from.
	b, err := json.Marshal(struct {
		Kind   string                `json:"kind"`
		Spec   json.RawMessage       `json:"spec"`
		Inputs map[wb.Address]string `json:"inputs"`
	}{n.Kind, n.Spec, inputs})
	if err != nil {
		// Spec was produced by json.Marshal or validated by json.Unmarshal.
		panic(fmt.Sprintf("engine: fingerprint %s: %v", n.Address, err))
	}
	return foundation.Digest(b)
}

// runs is a task's execution history: its newest start-or-finish record
// and its newest successful finish.
type runs struct{ latest, ok *foundation.Record }

func history(recs []foundation.Record) map[wb.Address]runs {
	h := map[wb.Address]runs{}
	for i := range recs {
		r := &recs[i]
		if r.Event != foundation.EventStart && r.Event != foundation.EventFinish {
			continue
		}
		cur := h[wb.Address(r.Subject)]
		cur.latest = r
		if r.Event == foundation.EventFinish && r.Status == foundation.StatusOK {
			cur.ok = r
		}
		h[wb.Address(r.Subject)] = cur
	}
	return h
}

// Short abbreviates a digest for display.
func Short(d string) string {
	if d == "" {
		return "none"
	}
	const keep = len("sha256:") + 12
	if len(d) <= keep {
		return d
	}
	return d[:keep]
}

// Describe renders a state briefly: "absent" or a short digest.
func Describe(s State) string {
	if !s.Exists {
		return "absent"
	}
	return Short(s.Digest)
}
