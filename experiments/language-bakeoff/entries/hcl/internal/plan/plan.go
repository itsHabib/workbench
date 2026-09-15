// Package plan turns normalized intent plus fresh observations and retained
// evidence into proposed changes. A plan is planner state: derived,
// disposable, never consulted as a fact about the world. It records what it
// observed so a later apply can refuse to act on a world that moved.
package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/adapter"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/evidence"
	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/intent"
)

// Action is what apply will do for one node.
type Action string

// The actions. There is deliberately no delete: wb never removes files.
const (
	Create Action = "create"
	Update Action = "update"
	Run    Action = "run"
	Keep   Action = "unchanged"
)

// StateKey is the Observed key holding a resource's fingerprint.
const StateKey = "state"

// Step is one node's proposed change and the observations that justify it.
type Step struct {
	Address string       `json:"address"`
	Kind    adapter.Kind `json:"kind"`
	Action  Action       `json:"action"`
	Reasons []string     `json:"reasons,omitempty"`
	Detail  []string     `json:"detail,omitempty"`
	Deps    []string     `json:"deps,omitempty"`
	// Observed is the precondition: the backend facts and receipt planning
	// saw. Apply refuses a saved plan when a fresh plan observes otherwise.
	Observed map[string]string `json:"observed"`
	// Want is the fingerprint a resource must report after apply.
	Want string `json:"want,omitempty"`
}

// Plan is the ordered set of steps for one source revision.
type Plan struct {
	Version      int    `json:"version"`
	SourceDigest string `json:"source_digest"`
	Steps        []Step `json:"steps"`
}

// Env is everything planning reads. Planning never writes.
type Env struct {
	Registry  *adapter.Registry
	Workspace adapter.Workspace
	Journal   *evidence.Journal
}

// Make plans in.Nodes in dependency order.
func Make(in *intent.Intent, env Env) (*Plan, error) {
	p := &Plan{Version: 1, SourceDigest: in.SourceDigest}
	changing := map[string]bool{}
	for _, n := range in.Nodes {
		s, err := planNode(n, in, env, changing)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", n.Address, err)
		}
		changing[n.Address] = s.Action != Keep
		p.Steps = append(p.Steps, s)
	}
	return p, nil
}

func planNode(n *intent.Node, in *intent.Intent, env Env, changing map[string]bool) (Step, error) {
	s := Step{Address: n.Address, Kind: n.Kind, Deps: n.Deps, Action: Keep}
	ra, isResource, err := resourceAdapter(n, env)
	if err != nil {
		return Step{}, err
	}
	if isResource {
		return planResource(s, n, ra, env.Workspace)
	}
	return planTask(s, n, in, env, changing)
}

func resourceAdapter(n *intent.Node, env Env) (adapter.ResourceAdapter, bool, error) {
	ad, ok := env.Registry.Lookup(n.Type)
	if !ok {
		return nil, false, fmt.Errorf("no adapter for %q", n.Type)
	}
	ra, ok := ad.(adapter.ResourceAdapter)
	return ra, ok && n.Kind == adapter.Resource, nil
}

// planResource compares the live observation with the desired fact. No
// record of earlier applies is consulted: the backend is the authority.
func planResource(s Step, n *intent.Node, ra adapter.ResourceAdapter, ws adapter.Workspace) (Step, error) {
	have, err := ra.Observe(ws, n.Attrs)
	if err != nil {
		return Step{}, err
	}
	want := ra.Want(n.Attrs)
	s.Observed = map[string]string{StateKey: have.Fingerprint}
	s.Want = want.Fingerprint
	if have.Fingerprint == want.Fingerprint {
		return s, nil
	}
	subject := strings.Join(n.Owns, ", ")
	s.Action = Create
	s.Reasons = []string{subject + ": " + want.Summary}
	if have.Exists {
		s.Action = Update
		s.Reasons = []string{subject + ": " + have.Summary + " -> " + want.Summary}
	}
	s.Detail = textDiff(have, want)
	return s, nil
}

func textDiff(have, want adapter.Fact) []string {
	if !want.HasText || (have.Exists && !have.HasText) {
		return nil
	}
	return lineDiff(have.Text, want.Text)
}

// taskView is what planning observed for a task.
type taskView struct {
	last     evidence.Entry // latest attempt: the receipt
	hasLast  bool
	lastOK   evidence.Entry // latest successful attempt: what wb last wrote
	inputs   map[string]string
	outputs  map[string]string
	upstream map[string]string
}

func planTask(s Step, n *intent.Node, in *intent.Intent, env Env, changing map[string]bool) (Step, error) {
	v, err := observeTask(n, in, env)
	if err != nil {
		return Step{}, err
	}
	s.Observed = v.flat()
	s.Reasons = staleReasons(n, v, changing)
	if len(s.Reasons) > 0 {
		s.Action = Run
	}
	s.Detail = append(unrecorded(v), abandoned(n, v)...)
	return s, nil
}

func observeTask(n *intent.Node, in *intent.Intent, env Env) (taskView, error) {
	v := taskView{}
	v.last, v.hasLast = env.Journal.Latest(n.Address)
	v.lastOK, _ = env.Journal.LatestSucceeded(n.Address)
	var err error
	if v.inputs, err = Digests(env.Workspace, n.Reads); err != nil {
		return v, err
	}
	if v.outputs, err = Digests(env.Workspace, n.Owns); err != nil {
		return v, err
	}
	v.upstream, err = Upstream(n, in, env)
	return v, err
}

// Upstream observes the current state of every block n references: a
// resource's fingerprint, or a task's output digests. A task's receipt
// records these, so "a referenced block changed" is judged from facts that
// survive a crash, not only from what the current plan intends to change.
func Upstream(n *intent.Node, in *intent.Intent, env Env) (map[string]string, error) {
	out := map[string]string{}
	for _, d := range n.Deps {
		fp, err := observeNode(in.Node(d), env)
		if err != nil {
			return nil, fmt.Errorf("observe upstream %s: %w", d, err)
		}
		out[d] = fp
	}
	return out, nil
}

func observeNode(n *intent.Node, env Env) (string, error) {
	if n == nil {
		return "", fmt.Errorf("not in the compiled intent")
	}
	ra, isResource, err := resourceAdapter(n, env)
	if err != nil {
		return "", err
	}
	if isResource {
		f, err := ra.Observe(env.Workspace, n.Attrs)
		return f.Fingerprint, err
	}
	digests, err := Digests(env.Workspace, n.Owns)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0, len(digests))
	for _, p := range sortedKeys(digests) {
		parts = append(parts, p+"="+digests[p])
	}
	return "outputs " + strings.Join(parts, ","), nil
}

// Digests observes each path's content fingerprint.
func Digests(ws adapter.Workspace, paths []string) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range paths {
		d, err := ws.Digest(p)
		if err != nil {
			return nil, err
		}
		out[p] = d
	}
	return out, nil
}

func (v taskView) flat() map[string]string {
	m := map[string]string{"receipt": "none"}
	if v.hasLast {
		m["receipt"] = fmt.Sprintf("#%d %s", v.last.Seq, v.last.Event)
	}
	for p, d := range v.inputs {
		m["input "+p] = d
	}
	for p, d := range v.outputs {
		m["output "+p] = d
	}
	return m
}

// Digest identifies the plan's exact content.
func (p *Plan) Digest() string {
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Changes counts steps that will cause effects.
func (p *Plan) Changes() int {
	n := 0
	for _, s := range p.Steps {
		if s.Action != Keep {
			n++
		}
	}
	return n
}

// Save writes the plan for a later `wb apply -plan`.
func (p *Plan) Save(path string) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// Load reads a saved plan.
func Load(path string) (*Plan, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Plan
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("saved plan: %w", err)
	}
	if p.Version != 1 {
		return nil, fmt.Errorf("saved plan: unsupported version %d", p.Version)
	}
	return &p, nil
}

// Short abbreviates a fingerprint for display.
func Short(fp string) string {
	algo, hexs, ok := strings.Cut(fp, ":")
	if !ok || len(hexs) <= 12 {
		return fp
	}
	return algo + ":" + hexs[:12]
}
