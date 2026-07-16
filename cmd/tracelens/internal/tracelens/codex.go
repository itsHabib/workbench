package tracelens

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

type codexEvent struct {
	Type  string     `json:"type"`
	Item  codexItem  `json:"item"`
	Error codexError `json:"error"`
}

type codexError struct {
	Message string `json:"message"`
}

type codexItem struct {
	ID               string           `json:"id"`
	Type             string           `json:"type"`
	Text             string           `json:"text"`
	Command          string           `json:"command"`
	AggregatedOutput string           `json:"aggregated_output"`
	ExitCode         *int             `json:"exit_code"`
	Status           string           `json:"status"`
	Changes          []map[string]any `json:"changes"`
}

// ParseCodexEvents decodes Codex JSON events as persisted by Ship. Started and
// completed command/file-change items pair by item id; agent messages become
// thought steps. Aggregate turn usage is intentionally not assigned to steps.
func ParseCodexEvents(r io.Reader) (Trajectory, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	b := codexBuilder{byItem: map[string]int{}}
	line := 0
	for sc.Scan() {
		line++
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var ev codexEvent
		if err := json.Unmarshal([]byte(text), &ev); err != nil {
			return Trajectory{}, fmt.Errorf("line %d: %w", line, err)
		}
		b.add(ev)
	}
	if err := sc.Err(); err != nil {
		return Trajectory{}, err
	}
	return b.finish()
}

type codexBuilder struct {
	steps   []Step
	byItem  map[string]int
	failure string
}

func (b *codexBuilder) add(ev codexEvent) {
	if ev.Type == "turn.failed" {
		b.addTurnFailure(ev.Error)
		return
	}
	if ev.Type != "item.started" && ev.Type != "item.completed" {
		return
	}
	if ev.Item.Type == "agent_message" {
		if ev.Type == "item.completed" && strings.TrimSpace(ev.Item.Text) != "" {
			b.steps = append(b.steps, Step{Role: "assistant", Thought: strings.TrimSpace(ev.Item.Text)})
		}
		return
	}
	if ev.Item.Type != "command_execution" && ev.Item.Type != "file_change" {
		return
	}
	i := b.itemStep(ev.Item)
	s := &b.steps[i]
	if ev.Type == "item.started" {
		return
	}
	ok := codexItemOK(ev.Item)
	s.OK = &ok
	if ok {
		s.Observation = codexObservation(ev.Item)
		return
	}
	s.Error = codexObservation(ev.Item)
}

// addTurnFailure records a turn.failed event as the producer's declared run
// failure and materializes it as a failed step, so a run codex itself declared
// failed cannot gate-pass on item exit codes alone.
func (b *codexBuilder) addTurnFailure(e codexError) {
	failed := false
	msg := e.Message
	if msg == "" {
		msg = "turn failed"
	}
	b.failure = msg
	b.steps = append(b.steps, Step{Role: "assistant", Tool: "turn", OK: &failed, Error: msg})
}

func (b *codexBuilder) itemStep(item codexItem) int {
	if i, ok := b.byItem[item.ID]; ok && item.ID != "" {
		return i
	}
	i := len(b.steps)
	b.steps = append(b.steps, Step{Role: "assistant", Tool: item.Type, Args: codexArgs(item)})
	if item.ID != "" {
		b.byItem[item.ID] = i
	}
	return i
}

func codexArgs(item codexItem) map[string]any {
	if item.Type == "command_execution" {
		return map[string]any{"command": item.Command}
	}
	if len(item.Changes) == 0 {
		return nil
	}
	return map[string]any{"changes": item.Changes}
}

func codexItemOK(item codexItem) bool {
	if item.ExitCode != nil {
		return *item.ExitCode == 0
	}
	return item.Status == "completed"
}

func codexObservation(item codexItem) string {
	if item.AggregatedOutput != "" {
		return item.AggregatedOutput
	}
	if len(item.Changes) > 0 {
		raw, err := json.Marshal(item.Changes)
		if err == nil {
			return string(raw)
		}
	}
	return item.Status
}

func (b *codexBuilder) finish() (Trajectory, error) {
	if len(b.steps) == 0 {
		return Trajectory{}, fmt.Errorf("no analyzable step events in codex stream")
	}
	for i := range b.steps {
		b.steps[i].Index = i
	}
	return Trajectory{Steps: b.steps, DeclaredFailure: b.failure}, nil
}
