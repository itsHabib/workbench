// Package contracts is the shared vocabulary of the workbench: the verdict
// schema every verifier emits and the artifact envelope every producer writes.
//
// It is a leaf. It imports nothing else in the module and carries no decision
// logic. Types and schema live here so tools stop hand-parsing one another's
// artifacts — but the reducer that composes verdicts, the ladder law it
// enforces, and every routing rule stay in the tools that own them. Share
// contracts, not call stacks.
//
// The behavioral source of truth for the verdict type is gate's internal/verify
// (type Verdict + Reduce). These types mirror that shape and are
// conformance-tested against the embedded schema; the reducer is deliberately
// absent, because composing verdicts is a decision and decisions do not live in
// a shared contract.
package contracts

// Verdict is the one artifact body every verifier emits — code, local-model, or
// judgment. Decision and Tier are orthogonal axes: decision says who may
// proceed (block > escalate > pass), tier says who must approve. Composition is
// monotone and lives in the producer's reducer, never here.
type Verdict struct {
	Subject    Subject   `json:"subject"`
	Source     string    `json:"source"`
	Producer   Producer  `json:"producer"`
	Decision   string    `json:"decision"`
	Tier       string    `json:"tier"`
	Confidence float64   `json:"confidence"`
	Findings   []Finding `json:"findings,omitempty"`
	Why        string    `json:"why"`
}

// Subject names what a verdict is about. PR-shaped today; a CI verdict is really
// about a run but reuses this subject.
type Subject struct {
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	HeadSHA string `json:"head_sha,omitempty"`
}

// Producer identifies who stands behind a verdict. Class carries the ladder
// semantics — the only values a reducer accepts; Impl names the specific
// implementation (a model, a binary, a person) for provenance only, and nothing
// may branch on it.
type Producer struct {
	Class string `json:"class"`
	Impl  string `json:"impl,omitempty"`
}

// Finding is one piece of a verdict's supporting evidence. Evidence carries the
// verbatim source line the finding quotes — the substrate a verbatim-evidence
// verifier checks and what explain shows — never a paraphrase.
type Finding struct {
	Title      string  `json:"title"`
	Severity   string  `json:"severity,omitempty"`
	Locus      string  `json:"locus,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
	Evidence   string  `json:"evidence,omitempty"`
}

// Producer classes — the ladder vocabulary. These are values, not logic: the
// semantics (a local-model producer may only pass or escalate; judgment cannot
// override a code block) live in the reducer that owns the decision.
const (
	ClassCode     = "code"
	ClassLocal    = "local-model"
	ClassJudgment = "judgment"
)

// Decisions, worst to best: block > escalate > pass.
const (
	DecisionBlock    = "block"
	DecisionEscalate = "escalate"
	DecisionPass     = "pass"
)
