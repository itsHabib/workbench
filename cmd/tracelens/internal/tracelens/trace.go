// Package tracelens analyzes agent run trajectories (JSONL steps) and diagnoses
// pathologies: loops, redundant tool calls, retry storms, cost hotspots, and
// stuck (no-progress) states. It is a composition of single-responsibility
// Detectors over a normalized Trajectory — pure computation, no network or
// credentials, so the analysis is reproducible and testable in isolation.
package tracelens

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Step is one turn of an agent loop: an optional thought, an optional tool call
// with arguments, the resulting observation, and per-step cost telemetry.
type Step struct {
	Index       int
	Role        string
	Thought     string
	Tool        string
	Args        map[string]any
	Observation string
	OK          *bool // nil when the step is not a tool result
	Error       string
	TokensIn    int
	TokensOut   int
	CostUSD     float64
	LatencyMS   int
}

// Trajectory is an ordered sequence of steps from a single agent run.
// DeclaredFailure carries a producer-declared terminal failure (codex
// turn.failed, claude result is_error) — the producer's own verdict that the
// run failed, independent of any per-step outcome; empty means none declared.
type Trajectory struct {
	Steps           []Step
	DeclaredFailure string
}

// IsTool reports whether the step invoked a tool.
func (s Step) IsTool() bool { return s.Tool != "" }

// Failed reports whether the step is a tool result that errored.
func (s Step) Failed() bool { return s.OK != nil && !*s.OK }

// callSig is the normalized identity of a tool call: tool name plus a stable
// JSON encoding of its arguments. Go's json.Marshal sorts map keys, so two
// calls with equivalent args produce byte-identical signatures. Absent args
// normalize to the empty object — a call recorded without args is the same
// call as one recorded with none. Args that fail to marshal keep a distinct
// sentinel so the failure never aliases an argless call.
func (s Step) callSig() string {
	if !s.IsTool() {
		return ""
	}
	if len(s.Args) == 0 {
		return s.Tool + "{}"
	}
	raw, err := json.Marshal(s.Args)
	if err != nil {
		raw = []byte("!marshal_error")
	}
	return s.Tool + string(raw)
}

// obsSig is the identity of a step's observed outcome: its ok flag plus the
// observation (or error) text. Two steps with the same obsSig produced the same
// information for the agent.
func (s Step) obsSig() string {
	body := s.Observation
	if body == "" {
		body = s.Error
	}
	ok := "?"
	if s.OK != nil {
		ok = fmt.Sprintf("%t", *s.OK)
	}
	return ok + "|" + body
}

// stateSig is the agent's state after a step, used by the progress model. It
// prefers the observed outcome, then the call, then the thought. An empty
// signature means the step carries no state signal and is skipped.
func (s Step) stateSig() string {
	if s.Observation != "" || s.Error != "" || s.OK != nil {
		return "obs:" + s.obsSig()
	}
	if s.IsTool() {
		return "call:" + s.callSig()
	}
	if s.Thought != "" {
		return "th:" + strings.TrimSpace(s.Thought)
	}
	return ""
}

// TotalCost sums per-step spend across the trajectory.
func (t Trajectory) TotalCost() float64 {
	var sum float64
	for _, s := range t.Steps {
		sum += s.CostUSD
	}
	return sum
}
