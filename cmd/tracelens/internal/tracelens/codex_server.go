package tracelens

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

type serverItem struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Text     string           `json:"text"`
	Command  string           `json:"command"`
	Output   string           `json:"aggregatedOutput"`
	ExitCode *int             `json:"exitCode"`
	Status   string           `json:"status"`
	Changes  []map[string]any `json:"changes"`
}

type serverEvent struct {
	Method string `json:"method"`
	Params struct {
		Thread string     `json:"threadId"`
		TurnID string     `json:"turnId"`
		Item   serverItem `json:"item"`
		Turn   struct {
			Status string     `json:"status"`
			Error  codexError `json:"error"`
		} `json:"turn"`
	} `json:"params"`
}

// ParseCodexServerEvents reads app-server notification traces. Tool items are
// scoped to thread and turn; incomplete or unrecognized outcomes stay unknown.
// This projection covers command execution, file changes, final agent text and
// explicit turn failure. It does not infer per-step costs from aggregate usage.
func ParseCodexServerEvents(r io.Reader) (Trajectory, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	b := codexBuilder{byItem: map[string]int{}}
	line := 0
	for scanner.Scan() {
		line++
		if len(scanner.Bytes()) == 0 {
			continue
		}
		var e serverEvent
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return Trajectory{}, fmt.Errorf("line %d: %w", line, err)
		}
		if err := addServerEvent(&b, e); err != nil {
			return Trajectory{}, fmt.Errorf("line %d: %w", line, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return Trajectory{}, err
	}
	return b.finish()
}

func addServerEvent(b *codexBuilder, e serverEvent) error {
	if e.Method == "turn/completed" && e.Params.Turn.Status == "failed" {
		b.addTurnFailure(e.Params.Turn.Error)
		return nil
	}
	if e.Method != "item/started" && e.Method != "item/completed" {
		return nil
	}
	i := e.Params.Item
	// Input and reasoning are not tool outcomes or final agent observations.
	if i.Type == "userMessage" || i.Type == "reasoning" {
		return nil
	}
	types := map[string]string{"commandExecution": "command_execution", "fileChange": "file_change", "agentMessage": "agent_message"}
	kind := types[i.Type]
	if kind == "" {
		return fmt.Errorf("unsupported app-server item type %q", i.Type)
	}
	key := i.ID
	if key != "" {
		key = e.Params.Thread + "/" + e.Params.TurnID + "/" + key
	}
	typ := "item.started"
	if e.Method == "item/completed" && (i.ExitCode != nil || i.Status == "completed" || i.Status == "failed" || kind == "agent_message") {
		typ = "item.completed"
	}
	b.add(codexEvent{Type: typ, Item: codexItem{ID: key, Type: kind, Text: i.Text, Command: i.Command, AggregatedOutput: i.Output, ExitCode: i.ExitCode, Status: i.Status, Changes: i.Changes}})
	return nil
}
