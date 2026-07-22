package tracelens

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Dialect identifies the producer format decoded into a Trajectory.
type Dialect string

// The dialects tracelens can decode.
const (
	DialectNeutral    Dialect = "neutral-jsonl"
	DialectShipCursor Dialect = "ship-cursor"
	DialectShipClaude Dialect = "ship-claude"
	DialectShipCodex  Dialect = "ship-codex"
)

// DecodedTrace carries the normalized trajectory and the provenance of the
// mechanism that decoded it. Detector policy remains dialect-neutral.
type DecodedTrace struct {
	Trajectory Trajectory
	Dialect    Dialect
}

// DecodeShipEvents detects and decodes one of the event dialects persisted by
// Ship. Detection fails closed on mixed, empty, or unrecognizable streams.
func DecodeShipEvents(r io.Reader) (DecodedTrace, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return DecodedTrace{}, err
	}
	dialect, err := detectShipDialect(raw)
	if err != nil {
		return DecodedTrace{}, err
	}
	var tr Trajectory
	switch dialect {
	case DialectShipCursor:
		tr, err = parseCursorEvents(bytes.NewReader(raw))
	case DialectShipClaude:
		tr, err = ParseClaudeEvents(bytes.NewReader(raw))
	case DialectShipCodex:
		tr, err = ParseCodexEvents(bytes.NewReader(raw))
	default:
		err = fmt.Errorf("unsupported ship dialect %q", dialect)
	}
	if err != nil {
		return DecodedTrace{}, err
	}
	return DecodedTrace{Trajectory: tr, Dialect: dialect}, nil
}

// DecodeTrace decodes a trace declared as the given dialect. Ship dialects are
// cross-checked against detection; the neutral path fails closed when the
// stream is actually a ship event dialect or carries no analyzable steps, so a
// misdeclared input cannot produce a fabricated clean verdict.
func DecodeTrace(r io.Reader, declared Dialect) (DecodedTrace, error) {
	if declared == DialectNeutral {
		return decodeNeutral(r)
	}
	decoded, err := DecodeShipEvents(r)
	if err != nil {
		return DecodedTrace{}, err
	}
	if decoded.Dialect != declared {
		return DecodedTrace{}, fmt.Errorf("declared dialect %q, detected %q", declared, decoded.Dialect)
	}
	return decoded, nil
}

func decodeNeutral(r io.Reader) (DecodedTrace, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return DecodedTrace{}, err
	}
	if dialect, err := detectShipDialect(raw); err == nil {
		return DecodedTrace{}, fmt.Errorf("declared dialect %q, detected %q", DialectNeutral, dialect)
	}
	tr, err := ParseJSONL(bytes.NewReader(raw))
	if err != nil {
		return DecodedTrace{}, err
	}
	if !hasAnalyzableStep(tr) {
		return DecodedTrace{}, fmt.Errorf("no analyzable steps in neutral trace")
	}
	return DecodedTrace{Trajectory: tr, Dialect: DialectNeutral}, nil
}

// hasAnalyzableStep guards the neutral path against streams whose lines
// unmarshal into empty steps (foreign schemas share no keys with the neutral
// one) — an all-empty trajectory must not certify a clean run.
func hasAnalyzableStep(tr Trajectory) bool {
	for _, s := range tr.Steps {
		if s.Tool != "" || s.Thought != "" {
			return true
		}
	}
	return false
}

type eventEnvelope struct {
	Type string `json:"type"`
}

// assistantCarriesToolUse reports whether a raw assistant event line carries at
// least one tool_use content block — the Claude-dialect signal that survives a
// run truncated before any system/user/result event. Any parse failure is a
// no (it simply falls through to the other dialect checks).
func assistantCarriesToolUse(line []byte) bool {
	var ev claudeEvent
	if err := json.Unmarshal(line, &ev); err != nil {
		return false
	}
	blocks, err := decodeClaudeBlocks(ev.Message.Content)
	if err != nil {
		return false
	}
	for _, blk := range blocks {
		if blk.Type == "tool_use" {
			return true
		}
	}
	return false
}

func detectShipDialect(raw []byte) (Dialect, error) {
	seen := map[Dialect]bool{}
	lines := bytes.Split(raw, []byte("\n"))
	meaningful := 0
	for i, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		meaningful++
		var ev eventEnvelope
		if err := json.Unmarshal(line, &ev); err != nil {
			return "", fmt.Errorf("line %d: %w", i+1, err)
		}
		// ship.* control events (ship.resumed) and assistant envelopes are
		// dialect-neutral: ship emits ship.* markers into every runtime's
		// stream, and both the cursor and claude dialects carry assistant
		// text events.
		switch {
		case ev.Type == "tool_call" || ev.Type == "thinking" || ev.Type == "status":
			seen[DialectShipCursor] = true
		case ev.Type == "system" || ev.Type == "result" || ev.Type == "user":
			seen[DialectShipClaude] = true
		case ev.Type == "assistant" && assistantCarriesToolUse(line):
			// A bare assistant text event stays dialect-neutral, but an
			// assistant event carrying a tool_use block is Claude-specific
			// (cursor emits a distinct tool_call event; codex uses item.*).
			// Keying on it lets a Claude run truncated right after a tool_use —
			// with no surviving system/user/result — still detect and decode
			// as an aborted run instead of erroring as an unrecognized dialect.
			seen[DialectShipClaude] = true
		case strings.HasPrefix(ev.Type, "thread.") || strings.HasPrefix(ev.Type, "turn.") || strings.HasPrefix(ev.Type, "item."):
			seen[DialectShipCodex] = true
		}
	}
	if meaningful == 0 {
		return "", fmt.Errorf("empty ship event stream")
	}
	if len(seen) == 0 {
		return "", fmt.Errorf("unrecognized ship event dialect")
	}
	if len(seen) > 1 {
		var names []string
		for d := range seen {
			names = append(names, string(d))
		}
		sort.Strings(names)
		return "", fmt.Errorf("mixed ship event dialects: %s", strings.Join(names, ", "))
	}
	for d := range seen {
		return d, nil
	}
	panic("unreachable")
}
