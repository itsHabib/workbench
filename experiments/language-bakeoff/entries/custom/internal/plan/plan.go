// Package plan turns an intent plus backend observations into a readable,
// savable plan, and applies a plan only while the workspace still matches
// what the plan observed.
package plan

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/foundation"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/intent"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/kind"
)

// Format versions the saved plan layout.
const Format = 1

// Work actions. Kept resources use the kind.Action values.
const (
	RunNow = "run"    // the work is stale and will run
	RunIf  = "run-if" // the work waits on upstream work; apply decides
	Skip   = "skip"   // the work is current
)

// Plan is a proposal to converge a workspace, plus the facts it was
// computed from. It carries its intent, so applying needs no source.
type Plan struct {
	Format  int           `json:"format"`
	Source  string        `json:"source"`
	Text    string        `json:"text,omitempty"` // the source text, so a saved plan can be re-checked
	Decls   []intent.Decl `json:"decls"`
	Changes []Change      `json:"changes"` // one per declaration, same order
	Checks  []Check       `json:"checks"`  // facts observed at plan time
	Notes   []string      `json:"notes,omitempty"`
}

// Change is the proposal for one declaration.
type Change struct {
	ID        string   `json:"id"`
	Lifecycle string   `json:"lifecycle"`
	Kind      string   `json:"kind"`
	Action    string   `json:"action"`
	Summary   string   `json:"summary"`
	Reasons   []string `json:"reasons,omitempty"`
	Detail    []string `json:"detail,omitempty"`
	Version   string   `json:"version,omitempty"` // what dependents will observe, when known
}

// Check is one fact the plan depends on. Apply refuses if any differs.
type Check struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Value   string `json:"value"`
}

// Make observes the workspace and proposes one change per declaration. It
// only reads: planning never writes to the workspace or its journal.
func Make(in *intent.Intent, reg *kind.Registry, ws *foundation.Workspace) (*Plan, error) {
	j, err := ws.Journal()
	if err != nil {
		return nil, fmt.Errorf("read journal: %w", err)
	}
	s := newState(reg, ws, j)
	p := &Plan{Format: Format, Source: in.Source, Decls: in.Decls}
	for _, d := range in.Decls {
		pr, err := s.propose(d)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", d.ID, err)
		}
		p.Changes = append(p.Changes, pr.change)
		p.Checks = append(p.Checks, pr.checks...)
	}
	if n := j.Skipped(); n > 0 {
		p.Notes = append(p.Notes, sprintf("the journal has %d unreadable line(s), ignored; a torn finish record reads as an interrupted attempt", n))
	}
	return p, nil
}

// state is the planner's working memory for one pass. It is rebuilt from
// backend facts every time and never saved.
type state struct {
	reg      *kind.Registry
	ws       *foundation.Workspace
	hist     map[string]*entry
	versions map[string]string // what each settled declaration provides to dependents
	pending  map[string]bool   // work that will run; its output is unknown until it does
}

func newState(reg *kind.Registry, ws *foundation.Workspace, j *foundation.Journal) *state {
	return &state{reg: reg, ws: ws, hist: history(j.Records()), versions: map[string]string{}, pending: map[string]bool{}}
}

// settled records work that just ran, so its dependents see its output.
func (s *state) settled(id, version string) {
	s.versions[id] = version
	delete(s.pending, id)
}

type proposal struct {
	change Change
	checks []Check
	inputs map[string]string // work: the input versions a start record keeps
}

func (s *state) propose(d intent.Decl) (proposal, error) {
	switch kind.Lifecycle(d.Lifecycle) {
	case kind.Keep:
		return s.proposeKeep(d)
	case kind.Run:
		return s.proposeRun(d)
	}
	return proposal{}, fmt.Errorf("unknown lifecycle %q", d.Lifecycle)
}

func (s *state) proposeKeep(d intent.Decl) (proposal, error) {
	res, ok := s.reg.Resource(d.Kind)
	if !ok {
		return proposal{}, fmt.Errorf("no resource adapter registered for kind %q", d.Kind)
	}
	now, err := res.Observe(s.ws, d)
	if err != nil {
		return proposal{}, err
	}
	prop, err := res.Propose(s.ws, d, now)
	if err != nil {
		return proposal{}, err
	}
	s.versions[d.ID] = prop.Version
	ch := newChange(d, string(prop.Action), prop.Summary)
	ch.Reasons = s.hist[d.ID].keepReasons(prop.Action, now)
	ch.Detail, ch.Version = prop.Detail, prop.Version
	return proposal{change: ch, checks: []Check{{d.ID, now.Subject, now.Digest}}}, nil
}

// keepReasons uses evidence to say who moved a resource away from its
// desired state: the source, or someone outside wb.
func (e *entry) keepReasons(a kind.Action, now kind.Fact) []string {
	if e == nil || e.written == nil || (a != kind.Create && a != kind.Update) {
		return nil
	}
	w := e.written
	switch now.Digest {
	case w.After:
		return []string{sprintf("the source changed since wb wrote %s at #%d", now.Subject, w.Seq)}
	case foundation.Absent:
		return []string{sprintf("%s was removed outside wb after #%d", now.Subject, w.Seq)}
	}
	return []string{sprintf("%s changed outside wb after #%d; applying overwrites that edit", now.Subject, w.Seq)}
}

func (s *state) proposeRun(d intent.Decl) (proposal, error) {
	w, ok := s.reg.Work(d.Kind)
	if !ok {
		return proposal{}, fmt.Errorf("no work adapter registered for kind %q", d.Kind)
	}
	out, err := w.Observe(s.ws, d)
	if err != nil {
		return proposal{}, err
	}
	in, err := s.inputs(d, w.Schema())
	if err != nil {
		return proposal{}, err
	}
	e := s.hist[d.ID]
	action, reasons := decide(d.Digest(), out, in, e)
	ch := newChange(d, action, runSummary(action, out))
	ch.Reasons = reasons
	if action == Skip {
		s.versions[d.ID] = out.Digest
		ch.Version = out.Digest
	}
	if action != Skip {
		s.pending[d.ID] = true
	}
	checks := append([]Check{{d.ID, out.Subject, out.Digest}, {d.ID, foundation.JournalPath, e.seqLabel()}}, in.checks...)
	return proposal{change: ch, checks: checks, inputs: in.versions}, nil
}

// blocked reports an output wb must not replace: work writes regular files,
// so a directory or a symlink at its output path is refused, as it is for
// a kept file.
func blocked(out kind.Fact) bool {
	return out.Digest == foundation.Dir || out.Digest == foundation.Other
}

// inputSet is what work depends on: the version of each upstream
// declaration and the digest of each literal path it reads. Upstream work
// that will run first has no version yet and is listed in waits.
type inputSet struct {
	versions map[string]string
	waits    []string
	checks   []Check
}

func (s *state) inputs(d intent.Decl, schema kind.Schema) (inputSet, error) {
	in := inputSet{versions: map[string]string{}}
	for _, up := range d.Needs {
		if s.pending[up] {
			in.waits = append(in.waits, up)
			continue
		}
		in.versions[up] = s.versions[up]
	}
	for _, f := range schema.Fields {
		p := d.One(f.Name)
		if f.Type != kind.Path || p == "" || d.Refs[f.Name] != "" {
			continue
		}
		digest, _, err := s.ws.ObserveTarget(p) // follow links: what the command reads
		if err != nil {
			return inputSet{}, err
		}
		in.versions["path:"+p] = digest
		in.checks = append(in.checks, Check{d.ID, p, digest})
	}
	return in, nil
}

// decide applies the replay rule: work runs when it never completed, when
// its definition or any input differs from its last receipt, or when its
// owned output no longer matches that receipt. Otherwise it is current.
// Work whose output path holds something it must not replace is a conflict.
func decide(def string, out kind.Fact, in inputSet, e *entry) (string, []string) {
	if blocked(out) {
		return string(kind.Conflict), []string{sprintf("%s is %s, not a regular file; wb will not replace it", out.Subject, describeBlocked(out))}
	}
	if reasons := staleness(def, out, in, e); len(reasons) > 0 {
		return RunNow, append(reasons, e.unfinished()...)
	}
	if len(in.waits) > 0 {
		return RunIf, []string{sprintf("waits for %s; runs only if that changes its input", strings.Join(in.waits, ", "))}
	}
	return Skip, []string{sprintf("definition, inputs and output match receipt #%d", e.success.finish.Seq)}
}

func staleness(def string, out kind.Fact, in inputSet, e *entry) []string {
	if e == nil || e.success == nil {
		return []string{"it never completed"}
	}
	ran := e.success.start
	var r []string
	if ran.Def != def {
		r = append(r, "its definition changed")
	}
	r = append(r, inputChanges(ran.Inputs, in)...)
	if fin := e.success.finish; out.Digest != fin.After {
		r = append(r, outputChange(out, fin))
	}
	return r
}

func inputChanges(was map[string]string, in inputSet) []string {
	var r []string
	for _, k := range sortedKeys(in.versions) {
		old, ok := was[k]
		if !ok {
			r = append(r, sprintf("new input %s", k))
			continue
		}
		if old != in.versions[k] {
			r = append(r, sprintf("input %s changed (%s -> %s)", k, Short(old), Short(in.versions[k])))
		}
	}
	for _, k := range sortedKeys(was) {
		if _, ok := in.versions[k]; !ok && !slices.Contains(in.waits, k) {
			r = append(r, sprintf("input %s was removed", k))
		}
	}
	return r
}

func outputChange(out kind.Fact, fin foundation.Record) string {
	if out.Digest == foundation.Absent {
		return sprintf("output %s is missing", out.Subject)
	}
	return sprintf("output %s does not match receipt #%d (found %s, recorded %s)", out.Subject, fin.Seq, Short(out.Digest), Short(fin.After))
}

func describeBlocked(out kind.Fact) string {
	if out.Digest == foundation.Dir {
		return "a directory"
	}
	return "a symlink or special file"
}

func runSummary(action string, out kind.Fact) string {
	switch action {
	case RunNow:
		return "run, writes " + out.Subject
	case RunIf:
		return "decided during apply, writes " + out.Subject
	case string(kind.Conflict):
		return "cannot write " + out.Subject
	}
	return "current, keeps " + out.Subject
}

func (e *entry) seqLabel() string {
	if e == nil {
		return "none"
	}
	return sprintf("#%d", e.lastSeq)
}

func newChange(d intent.Decl, action, summary string) Change {
	return Change{ID: d.ID, Lifecycle: d.Lifecycle, Kind: d.Kind, Action: action, Summary: summary}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Short abbreviates a digest for people; other values pass through.
func Short(v string) string {
	hex, ok := strings.CutPrefix(v, "sha256:")
	if !ok || len(hex) < 12 {
		return v
	}
	return hex[:12]
}

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
