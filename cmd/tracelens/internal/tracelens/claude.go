package tracelens

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type claudeEvent struct {
	Type    string        `json:"type"`
	Subtype string        `json:"subtype"`
	IsError bool          `json:"is_error"`
	Result  string        `json:"result"`
	Message claudeMessage `json:"message"`
}

type claudeMessage struct {
	Content json.RawMessage `json:"content"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     map[string]any  `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// ParseClaudeEvents decodes Claude Code stream-json as persisted by Ship.
// Assistant tool_use blocks start calls; user tool_result blocks complete them.
func ParseClaudeEvents(r io.Reader) (Trajectory, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	b := claudeBuilder{byCall: map[string]int{}}
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var ev claudeEvent
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

type claudeBuilder struct {
	steps   []Step
	byCall  map[string]int
	failure string
}

func (b *claudeBuilder) add(ev claudeEvent) error {
	if ev.Type == "result" {
		b.addRunResult(ev)
		return nil
	}
	if ev.Type != "assistant" && ev.Type != "user" {
		return nil
	}
	blocks, err := decodeClaudeBlocks(ev.Message.Content)
	if err != nil {
		return err
	}
	for _, block := range blocks {
		b.addBlock(ev.Type, block)
	}
	return nil
}

// addRunResult reads the terminal result envelope's own verdict. A result
// reporting an error is the producer declaring the run failed — dropping it
// would let a failed run gate-pass on tool outcomes alone.
func (b *claudeBuilder) addRunResult(ev claudeEvent) {
	if !ev.IsError && !strings.HasPrefix(ev.Subtype, "error") {
		return
	}
	msg := ev.Result
	if msg == "" {
		msg = ev.Subtype
	}
	if msg == "" {
		msg = "run failed"
	}
	b.failure = msg
}

func decodeClaudeBlocks(raw json.RawMessage) ([]claudeBlock, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var blocks []claudeBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		return blocks, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return []claudeBlock{{Type: "text", Text: text}}, nil
	}
	return nil, fmt.Errorf("message content is neither block array nor string")
}

func (b *claudeBuilder) addBlock(eventType string, block claudeBlock) {
	switch block.Type {
	case "thinking":
		b.addThought(block.Thinking)
	case "text":
		if eventType == "assistant" {
			b.addThought(block.Text)
		}
	case "tool_use":
		b.addToolUse(block)
	case "tool_result":
		b.addResult(block)
	}
}

// addToolUse starts a call step. A repeated id (replayed or duplicated stream
// segment) reuses the existing step, matching the cursor and codex decoders,
// so its eventual tool_result pairs with one step instead of stranding others.
func (b *claudeBuilder) addToolUse(block claudeBlock) {
	if i, ok := b.byCall[block.ID]; ok && block.ID != "" {
		b.steps[i] = Step{Role: "assistant", Tool: block.Name, Args: block.Input}
		return
	}
	i := len(b.steps)
	b.steps = append(b.steps, Step{Role: "assistant", Tool: block.Name, Args: block.Input})
	if block.ID != "" {
		b.byCall[block.ID] = i
	}
}

func (b *claudeBuilder) addThought(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	b.steps = append(b.steps, Step{Role: "assistant", Thought: text})
}

func (b *claudeBuilder) addResult(block claudeBlock) {
	i, ok := b.byCall[block.ToolUseID]
	if !ok || block.ToolUseID == "" {
		b.addOrphanResult(block)
		return
	}
	body := decodeClaudeContent(block.Content)
	s := &b.steps[i]
	okResult := !block.IsError
	s.OK = &okResult
	if block.IsError {
		s.Observation = ""
		s.Error = body
		return
	}
	s.Error = ""
	s.Observation = body
}

// addOrphanResult materializes a tool_result with no paired tool_use (head
// truncation, resumed stream) as its own step, matching the cursor and codex
// decoders' terminal-only handling — failure evidence must not vanish.
func (b *claudeBuilder) addOrphanResult(block claudeBlock) {
	body := decodeClaudeContent(block.Content)
	okResult := !block.IsError
	s := Step{Role: "assistant", Tool: "tool_result", OK: &okResult}
	if block.IsError {
		s.Error = body
	}
	if !block.IsError {
		s.Observation = body
	}
	b.steps = append(b.steps, s)
}

func decodeClaudeContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var parts []string
		for _, block := range blocks {
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		}
		return strings.Join(parts, "")
	}
	return string(raw)
}

func (b *claudeBuilder) finish() (Trajectory, error) {
	// A declared failure with no steps is still a decodable run verdict — a
	// stream that aborted before any content must block, not decode-error.
	if len(b.steps) == 0 && b.failure == "" {
		return Trajectory{}, fmt.Errorf("no analyzable step events in claude stream")
	}
	for i := range b.steps {
		b.steps[i].Index = i
	}
	return Trajectory{Steps: b.steps, DeclaredFailure: b.failure}, nil
}
