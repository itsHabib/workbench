package tracelens

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// shipEvent mirrors one line of a ship run's events.ndjson in the cursor-agent
// dialect: tool_call lifecycle events keyed by call_id, plus streamed
// thinking/assistant text fragments. Unknown fields are ignored so producers
// may add their own without breaking ingestion.
type shipEvent struct {
	Type    string          `json:"type"`
	CallID  string          `json:"call_id"`
	Name    string          `json:"name"`
	Status  string          `json:"status"`
	Args    map[string]any  `json:"args"`
	Result  json.RawMessage `json:"result"`
	Text    string          `json:"text"`
	Message *shipMessage    `json:"message"`
}

type shipMessage struct {
	Content []shipContent `json:"content"`
}

type shipContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ParseShipEvents converts a ship run's persisted event stream into a
// Trajectory. Tool calls arrive as running/completed lifecycle events, freely
// interleaved when calls run in parallel; events pair by call_id into a single
// step at the position the call first appeared. Consecutive thinking (or
// assistant) events are fragments of one utterance and coalesce into a single
// thought step. Events the dialect doesn't define are skipped. Public callers
// enter through DecodeShipEvents, which fails closed on mixed dialects.
//
// The stream carries no per-step cost, token, or latency telemetry and no
// tool-failure marker beyond a terminal status, so cost-hotspot findings (and,
// until failures appear, retry storms) cannot fire on ship traces.
func ParseShipEvents(r io.Reader) (Trajectory, error) {
	decoded, err := DecodeShipEvents(r)
	if err != nil {
		return Trajectory{}, err
	}
	return decoded.Trajectory, nil
}

// parseCursorEvents converts Cursor's Ship event stream into a trajectory.
func parseCursorEvents(r io.Reader) (Trajectory, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	b := shipBuilder{byCall: map[string]int{}}
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var ev shipEvent
		if err := json.Unmarshal([]byte(text), &ev); err != nil {
			return Trajectory{}, fmt.Errorf("line %d: %w", line, err)
		}
		if err := b.add(ev); err != nil {
			return Trajectory{}, fmt.Errorf("line %d: %w", line, err)
		}
	}
	if err := sc.Err(); err != nil {
		return Trajectory{}, err
	}
	return b.finish()
}

// shipBuilder folds the event stream into steps: a call_id → step index for
// pairing tool lifecycle events, and an open run of text fragments awaiting
// a flush.
type shipBuilder struct {
	steps    []Step
	byCall   map[string]int
	textKind string
	text     strings.Builder
}

func (b *shipBuilder) add(ev shipEvent) error {
	switch ev.Type {
	case "thinking":
		b.addChunk("thinking", ev.Text)
	case "assistant":
		b.addChunk("assistant", messageText(ev.Message))
	case "tool_call":
		b.addToolCall(ev)
	}
	return nil
}

// addChunk appends a streamed text fragment, flushing first when the fragment
// starts a different kind of utterance.
func (b *shipBuilder) addChunk(kind, text string) {
	if text == "" {
		return
	}
	if b.textKind != kind {
		b.flushText()
		b.textKind = kind
	}
	b.text.WriteString(text)
}

// flushText closes the open run of text fragments into one thought step.
func (b *shipBuilder) flushText() {
	t := strings.TrimSpace(b.text.String())
	b.text.Reset()
	b.textKind = ""
	if t == "" {
		return
	}
	b.steps = append(b.steps, Step{Role: "assistant", Thought: t})
}

func (b *shipBuilder) addToolCall(ev shipEvent) {
	b.flushText()
	i := b.callStep(ev.CallID)
	b.mergeCall(&b.steps[i], ev)
}

// callStep returns the step index for a call id, creating the step on first
// sight. Events without a call id can't pair, so each gets its own step.
func (b *shipBuilder) callStep(id string) int {
	if i, ok := b.byCall[id]; ok && id != "" {
		return i
	}
	i := len(b.steps)
	b.steps = append(b.steps, Step{})
	if id != "" {
		b.byCall[id] = i
	}
	return i
}

// mergeCall folds one lifecycle event into a call's step. A terminal status
// decides the outcome: completed calls succeeded, failed/error calls carry
// their text as the error. A running-only call keeps OK nil — it never
// reported back (interrupted run or a stream cut mid-call).
func (b *shipBuilder) mergeCall(s *Step, ev shipEvent) {
	if ev.Name != "" {
		s.Tool = ev.Name
	}
	if len(ev.Args) > 0 {
		s.Args = ev.Args
	}
	switch ev.Status {
	case "completed":
		ok := true
		s.OK = &ok
		s.Observation = decodeResult(ev.Result)
	case "failed", "error":
		ok := false
		s.OK = &ok
		s.Error = decodeResult(ev.Result)
	}
}

// decodeResult renders a tool result — a JSON string in practice, any JSON
// value defensively — as text.
func decodeResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// messageText concatenates the text blocks of a streamed assistant message.
func messageText(m *shipMessage) string {
	if m == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range m.Content {
		if c.Type != "text" {
			continue
		}
		b.WriteString(c.Text)
	}
	return b.String()
}

func (b *shipBuilder) finish() (Trajectory, error) {
	b.flushText()
	if len(b.steps) == 0 {
		return Trajectory{}, fmt.Errorf("no analyzable step events in cursor stream")
	}
	for i := range b.steps {
		b.steps[i].Index = i
	}
	return Trajectory{Steps: b.steps}, nil
}
