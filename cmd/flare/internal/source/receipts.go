package source

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
)

type receipt struct {
	Key        string `json:"key"`
	Source     string `json:"source"`
	Outcome    string `json:"outcome"`
	Project    string `json:"project"`
	Repo       string `json:"repo"`
	TaskSlug   string `json:"task_slug"`
	PRNumber   int    `json:"pr_number"`
	TerminalAt string `json:"terminal_at"`
}

// parseReceipts lifts failed and cancelled ship receipts. A receipt's key
// repeats across outcomes (pending -> merged), so the dedupe ID is
// key+outcome. Receipts carry no hash chain; the returned last-hash is "".
func parseReceipts(src config.Source, lines []string) ([]event.Event, string, error) {
	var events []event.Event
	for _, l := range lines {
		var r receipt
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			return nil, "", fmt.Errorf("source %s: bad receipt line: %w", src.Name, err)
		}
		ev, ok := receiptEvent(src, r)
		if !ok {
			continue
		}
		events = append(events, ev)
	}
	return events, "", nil
}

func receiptEvent(src config.Source, r receipt) (event.Event, bool) {
	sev, ok := receiptSeverity(r.Outcome)
	if !ok {
		return event.Event{}, false
	}
	when, _ := time.Parse(time.RFC3339, r.TerminalAt)
	what := r.TaskSlug
	if what == "" {
		what = r.Key
	}
	body := fmt.Sprintf("run %s %s", what, r.Outcome)
	if r.Repo != "" {
		body = fmt.Sprintf("%s: %s", r.Repo, body)
	}
	return event.Event{
		Source:   src.Name,
		ID:       r.Key + ":" + r.Outcome,
		Kind:     "receipt",
		Time:     when,
		Severity: sev,
		Title:    fmt.Sprintf("%s: %s %s", src.Name, what, r.Outcome),
		Body:     body,
		Fields:   map[string]string{"outcome": r.Outcome},
	}, true
}

func receiptSeverity(outcome string) (event.Severity, bool) {
	switch outcome {
	case "failed":
		return event.SevFailed, true
	case "cancelled":
		return event.SevCancelled, true
	}
	return 0, false
}
