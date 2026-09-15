package source

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
)

type receipt struct {
	Key         string `json:"key"`
	Source      string `json:"source"`
	Outcome     string `json:"outcome"`
	Project     string `json:"project"`
	Repo        string `json:"repo"`
	TaskSlug    string `json:"task_slug"`
	PRNumber    int    `json:"pr_number"`
	GeneratedAt string `json:"generated_at"`
	TerminalAt  string `json:"terminal_at"`
}

// parseReceipts lifts failed, cancelled, and parked ship receipts. A receipt's key
// repeats across outcomes (pending -> merged), so the dedupe ID is
// key+outcome. Receipts carry no hash chain; the returned last-hash is "".
func parseReceipts(src config.Source, lines []string) ([]event.Event, string, error) {
	var events []event.Event
	for _, l := range lines {
		var r receipt
		if err := json.Unmarshal([]byte(l), &r); err != nil {
			return nil, "", fmt.Errorf("source %s: bad receipt line: %w", src.Name, err)
		}
		ev, ok, err := receiptEvent(src, r)
		if err != nil {
			return nil, "", err
		}
		if !ok {
			continue
		}
		events = append(events, ev)
	}
	return events, "", nil
}

func receiptEvent(src config.Source, r receipt) (event.Event, bool, error) {
	sev, ok := receiptSeverity(r.Outcome)
	if !ok {
		return event.Event{}, false, nil
	}
	when, err := receiptTime(r)
	if err != nil {
		return event.Event{}, false, fmt.Errorf("source %s: receipt %q: %w", src.Name, r.Key, err)
	}
	what := r.TaskSlug
	if what == "" {
		what = r.Key
	}
	body := fmt.Sprintf("run %s %s", what, r.Outcome)
	if r.Outcome == "parked" {
		body = fmt.Sprintf("run %s paused after a failure or dispatch ambiguity; inspect Ship to retry, skip, abort, or adopt", what)
	}
	if r.Repo != "" {
		body = fmt.Sprintf("%s: %s", r.Repo, body)
	}
	return event.Event{
		Source:   src.Name,
		ID:       r.Key + ":" + r.Outcome,
		Kind:     event.KindReceipt,
		Time:     when,
		Severity: sev,
		Title:    fmt.Sprintf("%s: %s %s", src.Name, what, r.Outcome),
		Body:     body,
		Fields:   receiptFields(r),
	}, true, nil
}

func receiptTime(r receipt) (time.Time, error) {
	raw := r.GeneratedAt
	field := "generated_at"
	if raw == "" {
		raw = r.TerminalAt
		field = "terminal_at"
	}
	if raw == "" {
		return time.Time{}, fmt.Errorf("missing generated_at (and historical terminal_at fallback)")
	}
	when, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad %s: %w", field, err)
	}
	return when, nil
}

// receiptFields carries the receipt's structured facts so notify can render
// clean fields and a PR link. Routes still select on "outcome".
func receiptFields(r receipt) map[string]string {
	fields := map[string]string{"outcome": r.Outcome}
	if r.Repo != "" {
		fields["repo"] = r.Repo
	}
	if r.PRNumber > 0 {
		fields["number"] = strconv.Itoa(r.PRNumber)
	}
	if r.TaskSlug != "" {
		fields["task"] = r.TaskSlug
	}
	return fields
}

func receiptSeverity(outcome string) (event.Severity, bool) {
	switch outcome {
	case "failed":
		return event.SevFailed, true
	case "cancelled":
		return event.SevCancelled, true
	case "parked":
		// Ship emits park receipts for failure triage and dispatch ambiguity.
		// Those need attention in Ship, but they are not a Gate policy park that
		// can be approved or blocked through escalate serve.
		return event.SevFailed, true
	}
	return 0, false
}
