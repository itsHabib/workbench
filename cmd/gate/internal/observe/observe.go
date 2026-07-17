// Package observe is read-only and storeless. It renders explanations and
// audits purely from state artifacts — if anything here needed a side channel
// (prose, process memory, path conventions), the substrate contract would be
// leaking.
package observe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/itsHabib/workbench/cmd/gate/internal/state"
)

// Run is a structured, read-only projection of one gate run's artifact chain.
type Run struct {
	Run       string `json:"run"`
	Artifacts []Node `json:"artifacts"`
}

// Node is one artifact in a run projection, with kind-specific fields extracted
// from the body. Unparseable bodies are flagged explicitly rather than dropped.
type Node struct {
	ID       string           `json:"id"`
	Kind     string           `json:"kind"`
	Time     string           `json:"time"`
	Parents  []string         `json:"parents,omitempty"`
	Evidence *EvidenceSummary `json:"evidence,omitempty"`
	Verdict  *VerdictSummary  `json:"verdict,omitempty"`
	// Flat marshals in Go's map order (alphabetical keys) — key order carries
	// no meaning in the JSON projection.
	Flat map[string]any `json:"flat,omitempty"`
	// flatOrder preserves the artifact's own key order for text rendering. It
	// is populated only by Project and never serialized, so a Node decoded
	// from JSON cannot be text-rendered.
	flatOrder   []flatKV
	Unparseable bool `json:"unparseable,omitempty"`
}

type flatKV struct {
	key string
	val any
}

// EvidenceSummary captures what explain shows for an evidence artifact.
type EvidenceSummary struct {
	Type      string `json:"type"`
	ByteCount int    `json:"byte_count,omitempty"`
	ItemCount int    `json:"item_count,omitempty"`
}

// VerdictSummary captures what explain shows for verdict and judgment artifacts.
type VerdictSummary struct {
	Source     string           `json:"source"`
	Producer   string           `json:"producer"`
	Decision   string           `json:"decision"`
	Tier       string           `json:"tier"`
	Confidence float64          `json:"confidence"`
	Why        string           `json:"why"`
	Findings   []FindingSummary `json:"findings,omitempty"`
}

// FindingSummary is one finding line in a verdict projection.
type FindingSummary struct {
	Title    string `json:"title"`
	Severity string `json:"severity,omitempty"`
	Locus    string `json:"locus,omitempty"`
	Evidence string `json:"evidence,omitempty"`
}

// Project builds a structured projection of run from the artifact log alone.
func Project(st *state.Store, run string) (Run, error) {
	arts, err := st.Run(run)
	if err != nil {
		return Run{}, err
	}
	if len(arts) == 0 {
		return Run{}, fmt.Errorf("observe: run %s has no artifacts", run)
	}
	out := Run{Run: run, Artifacts: make([]Node, len(arts))}
	for i, a := range arts {
		out.Artifacts[i] = projectNode(a)
	}
	return out, nil
}

// Explain reconstructs a gate run's decision chain from the artifact log alone.
func Explain(w io.Writer, st *state.Store, run string) error {
	proj, err := Project(st, run)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "run %s — %d artifacts\n\n", run, len(proj.Artifacts))
	for _, n := range proj.Artifacts {
		fmt.Fprintf(w, "%s  %s  %s\n", n.Time, n.Kind, n.ID)
		if len(n.Parents) > 0 {
			fmt.Fprintf(w, "         from: %v\n", n.Parents)
		}
		renderNode(w, n)
		fmt.Fprintln(w)
	}
	return nil
}

// ExplainJSON marshals the run projection as one JSON document.
func ExplainJSON(w io.Writer, st *state.Store, run string) error {
	proj, err := Project(st, run)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(proj)
}

func projectNode(a state.Artifact) Node {
	n := Node{
		ID:      a.ID,
		Kind:    a.Kind,
		Time:    a.Time.Format("15:04:05"),
		Parents: a.Parents,
	}
	switch a.Kind {
	case state.KindEvidence:
		projectEvidence(&n, a.Body)
	case state.KindVerdict, state.KindJudgment:
		projectVerdict(&n, a.Body)
	case state.KindGrant, state.KindEscalation, state.KindAction:
		projectFlat(&n, a.Body)
	}
	return n
}

func projectEvidence(n *Node, body json.RawMessage) {
	// Every evidence body carries "pr" only alongside one of the fields below,
	// so it is not decoded here.
	var b struct {
		Diff     string            `json:"diff"`
		Comments []json.RawMessage `json:"comments"`
		Data     json.RawMessage   `json:"data"`
		Runs     []json.RawMessage `json:"runs"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		n.Unparseable = true
		return
	}
	switch {
	case b.Diff != "":
		n.Evidence = &EvidenceSummary{Type: "diff", ByteCount: len(b.Diff)}
	case b.Comments != nil:
		n.Evidence = &EvidenceSummary{Type: "comments", ItemCount: len(b.Comments)}
	case b.Runs != nil:
		n.Evidence = &EvidenceSummary{Type: "ci-logs", ItemCount: len(b.Runs)}
	case b.Data != nil:
		n.Evidence = &EvidenceSummary{Type: "pr-view", ByteCount: len(b.Data)}
	}
}

func projectVerdict(n *Node, body json.RawMessage) {
	var v struct {
		Source   string `json:"source"`
		Producer struct {
			Class string `json:"class"`
			Impl  string `json:"impl"`
		} `json:"producer"`
		Decision   string  `json:"decision"`
		Tier       string  `json:"tier"`
		Confidence float64 `json:"confidence"`
		Why        string  `json:"why"`
		Findings   []struct {
			Title    string `json:"title"`
			Severity string `json:"severity"`
			Locus    string `json:"locus"`
			Evidence string `json:"evidence"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		n.Unparseable = true
		return
	}
	producer := v.Producer.Class
	if v.Producer.Impl != "" {
		producer += "/" + v.Producer.Impl
	}
	findings := make([]FindingSummary, len(v.Findings))
	for i, f := range v.Findings {
		findings[i] = FindingSummary{
			Title:    f.Title,
			Severity: f.Severity,
			Locus:    f.Locus,
			Evidence: f.Evidence,
		}
	}
	n.Verdict = &VerdictSummary{
		Source:     v.Source,
		Producer:   producer,
		Decision:   v.Decision,
		Tier:       v.Tier,
		Confidence: v.Confidence,
		Why:        v.Why,
		Findings:   findings,
	}
}

func projectFlat(n *Node, body json.RawMessage) {
	flat, order, err := parseFlatObject(body)
	if err != nil {
		n.Unparseable = true
		return
	}
	n.Flat = flat
	n.flatOrder = order
}

func parseFlatObject(body json.RawMessage) (map[string]any, []flatKV, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if tok != json.Delim('{') {
		return nil, nil, fmt.Errorf("observe: flat body is not an object")
	}
	flat := make(map[string]any)
	var order []flatKV
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, nil, fmt.Errorf("observe: flat body key is not a string")
		}
		var val any
		if err := dec.Decode(&val); err != nil {
			return nil, nil, err
		}
		flat[key] = val
		order = append(order, flatKV{key: key, val: val})
	}
	end, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if end != json.Delim('}') {
		return nil, nil, fmt.Errorf("observe: flat body does not end with }")
	}
	if dec.More() {
		return nil, nil, fmt.Errorf("observe: flat body has trailing data")
	}
	return flat, order, nil
}

func renderNode(w io.Writer, n Node) {
	if n.Unparseable {
		fmt.Fprintf(w, "         (unparseable body)\n")
		return
	}
	switch n.Kind {
	case state.KindEvidence:
		renderEvidence(w, n.Evidence)
	case state.KindVerdict, state.KindJudgment:
		renderVerdict(w, n.Verdict)
	case state.KindGrant, state.KindEscalation, state.KindAction:
		renderFlat(w, n)
	}
}

func renderEvidence(w io.Writer, e *EvidenceSummary) {
	if e == nil {
		return
	}
	switch e.Type {
	case "diff":
		fmt.Fprintf(w, "         diff evidence: %d bytes\n", e.ByteCount)
	case "comments":
		fmt.Fprintf(w, "         comments evidence: %d comments\n", e.ItemCount)
	case "ci-logs":
		fmt.Fprintf(w, "         ci-logs evidence: %d red runs\n", e.ItemCount)
	case "pr-view":
		fmt.Fprintf(w, "         pr-view evidence: %d bytes\n", e.ByteCount)
	}
}

func renderVerdict(w io.Writer, v *VerdictSummary) {
	if v == nil {
		return
	}
	fmt.Fprintf(w, "         %s [%s] → %s tier=%s conf=%.2f\n", v.Source, v.Producer, v.Decision, v.Tier, v.Confidence)
	fmt.Fprintf(w, "         why: %s\n", v.Why)
	for _, f := range v.Findings {
		head := strings.TrimSpace(f.Severity + " " + f.Locus)
		if head != "" {
			head += " "
		}
		fmt.Fprintf(w, "           - %s%s\n", head, f.Title)
		if f.Evidence != "" {
			fmt.Fprintf(w, "             evidence: %s\n", f.Evidence)
		}
	}
}

func renderFlat(w io.Writer, n Node) {
	for _, kv := range n.flatOrder {
		fmt.Fprintf(w, "         %s: %v\n", kv.key, kv.val)
	}
}
