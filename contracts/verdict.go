// Package contracts is the shared vocabulary of the workbench: the verdict
// schema every verifier emits and the artifact envelope every producer writes.
//
// It is a leaf. It imports nothing else in the module and carries no decision
// logic. Types and schema live here so tools stop hand-parsing one another's
// artifacts — but the reducer that composes verdicts, the ladder law it
// enforces, and every routing rule stay in the tools that own them. Share
// contracts, not call stacks.
//
// These are the canonical verdict types: gate's internal/verify — the
// behavioral source of truth for how verdicts compose — aliases them directly,
// so the emitter and the vocabulary cannot drift. They are conformance-tested
// against the embedded schema; the reducer is deliberately absent, because
// composing verdicts is a decision and decisions do not live in a shared
// contract.
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
	// Decider names who stands behind a JUDGMENT and how their identity was
	// established. Absent on code and local-model verdicts — a deterministic
	// verifier has no decider — and absent on judgments recorded before the
	// field existed. Provenance only: nothing may branch on it.
	Decider *Decider `json:"decider,omitempty"`
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

// Decider names the identity behind a JUDGMENT-class verdict and how that
// identity was established. A judgment is the one verdict a person or a
// delegated provider authors rather than a deterministic verifier computes, so
// it is the one verdict whose author must be nameable after the fact: without
// this, a human approval and an agent-composed one are indistinguishable in the
// record, and every operator approval names nobody.
//
// Provenance only, exactly like Producer.Impl — the reducer must never branch on
// it, and it carries no ladder semantics. It is optional on the wire so that
// judgments recorded before this field existed still decode; the WRITE path is
// where absence is refused, because a reader that rejected an unattributed
// legacy judgment could no longer explain the runs it needs to explain.
//
// Method distinguishes how the identity was established, not who vouches for it:
// gate authenticates none of these, so the field records the CHANNEL a decision
// arrived through (MethodCLIOperator, MethodSlackInteractive, MethodAuto+provider)
// and nothing stronger. Naming the channel is what lets a later reader tell a
// button tap on a phone from a shell an agent also had.
type Decider struct {
	// Who is the deciding identity: an operator's name for a human decision, or
	// the provider/model projection for a delegated one.
	Who string `json:"who"`
	// Method is the channel the decision arrived through — one of the Method
	// constants, or MethodAuto + a provider name.
	Method string `json:"method"`
	// At is when the decision was made, RFC 3339 UTC. It is the DECIDER's clock,
	// distinct from the artifact envelope's append time: a decision taken on a
	// phone and recorded minutes later has two different, both-true timestamps.
	At string `json:"at"`
}

// Decision methods — the channel vocabulary Decider.Method draws on. They name
// how an identity was established, never that it was authenticated; gate
// authenticates none of them.
const (
	// MethodCLIOperator is an operator running the gate CLI directly.
	MethodCLIOperator = "cli-operator"
	// MethodSlackInteractive is an operator tapping an Approve/Block button on a
	// Slack card, ingested through the escalate back-channel.
	MethodSlackInteractive = "slack-interactive"
	// MethodAuto prefixes a delegated auto-judgment: MethodAuto + the provider
	// name (e.g. "auto-claude"). The provider and model land in Who.
	MethodAuto = "auto-"
)

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
