// Package escalation is the cross-tool contract for one agent→human PUSH: the
// wire shape a gate run takes when it parks a pull request for judgment, the
// brief it carries for a zero-context approver, and — new — the resolution that
// closes the loop when a human's decision comes back.
//
// It is the typed replacement for an implicit contract that was triple-declared
// before: an untyped map[string]any on gate's write side, and a locally
// redeclared, tolerantly-decoded struct in TWO readers (flare's gate-log source
// and gate's own inbox projection). One source of truth now, mirroring
// contracts/authority and contracts/driverstate: share contracts, not call
// stacks.
//
// It is a leaf. It imports nothing else in the module and carries no decision
// logic: it never chooses to park (gate's ladder law), never routes (flare),
// never resolves (the back-channel + gate's judge mechanism). It is only the
// shared vocabulary — the ergonomic view of the embedded JSON Schema, which
// conformance_test.go keeps in lockstep.
//
// Readers are tolerant by design. DecodeBody ignores unknown additive fields
// and applies NO version gate, so the routing and projection readers still
// render a body that predates this typed contract — the migration must never
// break the existing best-effort route. Validate is the separate strict law (a
// version gate plus the required-field rule) a consumer applies when it wants
// teeth; the tolerant read path does not.
package escalation

import (
	"encoding/json"
	"fmt"
)

// V1 is one agent→human push: a gate run parked for judgment. Exactly
// one line per park. Self-contained enough that a cold reader — a notification
// sink, a projection, the resolution back-channel — answers what parked, why,
// under which grant and verdict, about which PR, briefed how, and (once a human
// acts) resolved how, from the body alone.
//
// The field set is a superset of the map[string]any gate wrote before, so the
// migration is non-breaking: every previously-persisted key keeps its json tag
// and meaning. SchemaVersion, RunID, and Resolution are the additive fields.
type V1 struct {
	SchemaVersion string `json:"schema_version"`
	// Outcome is gate's own outcome string for the park (today always
	// "parked_for_judgment"). It leads a notification's title, so it is a
	// required part of the wire shape, not decoration.
	Outcome string `json:"outcome"`
	// Verdict is the reduced verdict id the park stands on; Grant is the grant
	// the run ran under. Both are provenance the inbox reads to build a
	// paste-ready judge/resolve command, so both always marshal.
	Verdict string `json:"verdict"`
	Grant   string `json:"grant"`
	// Question is the park reason a zero-context reader sees first. Required: an
	// escalation with no question is a page with no ask.
	Question string `json:"question"`
	// RunID names the gate run this park belongs to. Optional and additive: the
	// artifact envelope already carries the run, but a self-contained body lets a
	// reader resolve the run without the envelope.
	RunID string `json:"run_id,omitempty"`
	// Code is the machine-readable park code (Code* below). Absent on a content
	// park — the one escalation that pages a human on findings, which carries a
	// Brief instead. Present on the procedural parks (authorization facts).
	Code string `json:"code,omitempty"`
	// Repo and Number name the PR the park is about, when it has one, so a sink
	// renders a direct click-target instead of a bare run id.
	Repo   string `json:"repo,omitempty"`
	Number int    `json:"number,omitempty"`
	// Brief is the synthesized plain-language page for a zero-context approver.
	// Optional: procedural parks and synthesis failures carry none, and the sink
	// falls back to the raw Question.
	Brief *Brief `json:"brief,omitempty"`
	// Escape is the runnable route Gate printed for this park. It is carried in
	// the hashed body so a later reader can reconstruct the same next step from
	// state alone rather than trusting terminal scrollback.
	Escape     *Escape `json:"escape,omitempty"`
	SelfGated  bool    `json:"self_gated,omitempty"`
	RetryHelps *bool   `json:"retry_helps,omitempty"`
	// Resolution is the closed-loop stamp — decision, who, when, and the judgment
	// it produced. It is NEVER written into the park artifact: the log is
	// append-only, so the park is immutable. The back-channel appends a STANDALONE
	// KindResolution artifact (whose body is a Resolution), and this embedded field
	// is the PROJECTED view a reader fills by joining that artifact back to the
	// escalation. So it is absent on every real park body today; a projection is
	// the only thing that ever populates it (deferred — see the plane spec).
	Resolution *Resolution `json:"resolution,omitempty"`
}

// Escape is objective terminal guidance recorded by Gate: why this route does
// not pass through the failed component and the one command to run next.
type Escape struct {
	Why  string `json:"why"`
	Next string `json:"next"`
}

// Brief is the one-screen page an escalation carries for a zero-context
// approver: what the PR is, the worry, how bad it would be, and a next step —
// translated from the findings, not the findings themselves. Advisory only.
// Its json tags mirror gate's verify.Brief exactly, so the write site maps one
// to the other field-for-field with no re-tagging.
type Brief struct {
	WhatItIs       string `json:"what_it_is"`
	Concern        string `json:"concern"`
	Risk           string `json:"risk"`
	Recommendation string `json:"recommendation"`
}

// Resolution is the decision a human returned for a parked escalation, recorded
// with provenance: what they decided, who they are, when, and the id of the
// judgment artifact the decision produced. It is the missing seam this contract
// formalizes — the agent→human→agent return path. It is the body of the
// standalone KindResolution artifact the back-channel appends (parented to the
// escalation it resolves and the judgment it links); a reader can also project
// it onto V1.Resolution by joining that artifact back to the park.
type Resolution struct {
	Decision   string `json:"decision"`
	Who        string `json:"who"`
	At         string `json:"at"`
	JudgmentID string `json:"judgment_id"`
}

// Park codes — the machine-readable Code enum. A content park carries no code
// (the field is omitted); these name the procedural parks. The string VALUES
// are the wire contract: they equal gate's own persisted park codes
// (capability.ErrTierCeiling.Error() and friends, plus gate's
// cycle_count_unreadable), so adopting this typed body changed no persisted
// value. A gate-side test pins that equality so the two can never drift.
const (
	CodeTierExceeded    = "grant_tier_exceeded"
	CodeCountUnreadable = "cycle_count_unreadable"
	CodeCycleExceeded   = "grant_cycle_exceeded"
)

// Resolution decisions — the closed vocabulary a human returns. They mirror
// gate's verdict decisions (pass/block); the back-channel maps its inbound
// decision onto one of these before recording it.
const (
	DecisionPass  = "pass"
	DecisionBlock = "block"
)

// Slack interactive-action ids — the vocabulary a Slack Approve/Block button
// carries when the operator resolves a parked escalation from the card. They
// are shared here for the SAME reason V1 is: two tools sit on opposite ends of
// this wire and must not drift. flare (the read-only router) RENDERS a button
// whose action_id is one of these and whose value is the escalation artifact
// id; escalate's serve transport PARSES that action_id back to a decision. Both
// import this leaf; neither imports the other (the boundary law). A literal
// "approve"/"block" duplicated on each side would be exactly the implicit,
// drift-prone contract this package exists to retire.
//
// The button's VALUE (not defined here — it is just the id) carries the
// escalation artifact id the resolution is keyed on, so a verified callback
// joins straight back to the parked run without the operator pasting anything.
//
// These are vocabulary only. The action_id → decision mapping is the parser's
// policy and lives with the transport that reads a callback (escalate's serve),
// not here — this leaf never decides, it only names.
const (
	ActionApprove = "approve"
	ActionBlock   = "block"
)

// DecodeBody tolerantly decodes an escalation artifact body: unknown fields
// (from an older or newer writer) are ignored, and NO version gate rejects —
// the routing and projection readers must still render a body that predates
// this typed contract. It returns an error only on malformed JSON, so a caller
// that wants the old best-effort behavior treats any error as "no body" and
// renders from what it has. This is the reader the collapse points flare and
// gate's inbox at.
func DecodeBody(raw []byte) (V1, error) {
	var e V1
	if err := json.Unmarshal(raw, &e); err != nil {
		return V1{}, fmt.Errorf("escalation: decode body: %w", err)
	}
	return e, nil
}
