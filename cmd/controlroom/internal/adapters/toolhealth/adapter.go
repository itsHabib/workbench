// Package toolhealth adapts the human-oriented tool-health board contract.
package toolhealth

import (
	"context"
	"errors"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

const defaultTimeout = 20 * time.Second

// Result is tool-health's source-local collection result.
type Result struct {
	Tools   []model.ToolHealth
	Receipt model.SourceReceipt
}

type runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, executable string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, executable, args...).Output()
}

// Adapter executes and parses the frozen board-text boundary.
type Adapter struct {
	executable string
	timeout    time.Duration
	runner     runner
	now        func() time.Time
}

// New constructs a tool-health adapter.
func New(executable string) *Adapter {
	return &Adapter{executable: executable, timeout: defaultTimeout, runner: commandRunner{}, now: time.Now}
}

// Collect executes exactly `<executable> board` once.
func (a *Adapter) Collect(ctx context.Context) Result {
	started := a.now()
	receipt := model.SourceReceipt{Source: "toolhealth", ObservedAt: started}
	finish := func(state model.SourceState, code, message string, rows []model.ToolHealth) Result {
		receipt.State, receipt.ErrorCode, receipt.Message = state, code, message
		receipt.DurationMS = max(0, a.now().Sub(started).Milliseconds())
		return Result{Tools: rows, Receipt: receipt}
	}
	if a.executable == "" {
		return finish(model.SourceUnavailable, "not_configured", "tool-health is not configured", nil)
	}

	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	stdout, err := a.runner.Run(callCtx, a.executable, "board")
	if err != nil {
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return finish(model.SourceUnavailable, "timeout", "tool-health collection timed out", nil)
		}
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return finish(model.SourceUnavailable, "executable_not_found", "tool-health executable is unavailable", nil)
		}
		return finish(model.SourceUnavailable, "command_failed", "tool-health collection failed", nil)
	}

	rows, partial, code := parseBoard(string(stdout))
	if code != "" {
		return finish(model.SourceUnavailable, code, "tool-health output no longer matches the accepted contract", nil)
	}
	if partial {
		return finish(model.SourceDegraded, "partial_parse", "some tool-health rows were incomplete", rows)
	}
	return finish(model.SourceOK, "", "", rows)
}

func parseBoard(text string) ([]model.ToolHealth, bool, string) {
	if !strings.Contains(text, "Tool Health Board") || !strings.Contains(text, "Generated:") {
		return nil, false, "contract_drift"
	}
	if strings.Contains(text, "!!! ACTIVE INCIDENT !!!") {
		return parseIncident(text)
	}
	return parseAccumulated(text)
}

func parseIncident(text string) ([]model.ToolHealth, bool, string) {
	required := []string{"Tool:", "Severity:", "Started:", "Status:", "Kind: live_incident"}
	for _, anchor := range required {
		if !strings.Contains(text, anchor) {
			return nil, false, "contract_drift"
		}
	}
	fields := make(map[string]string)
	incident := strings.SplitN(text, "!!! ACTIVE INCIDENT !!!", 2)[1]
	for _, line := range strings.Split(incident, "\n") {
		for _, key := range []string{"Tool", "Severity", "Started", "Status"} {
			prefix := key + ":"
			if strings.HasPrefix(strings.TrimSpace(line), prefix) {
				if _, exists := fields[key]; exists {
					return nil, false, "contract_drift"
				}
				fields[key] = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), prefix))
			}
		}
	}
	started, err := time.Parse(time.RFC3339Nano, fields["Started"])
	if err != nil || fields["Tool"] == "" || fields["Severity"] == "" || fields["Status"] == "" {
		return nil, false, "contract_drift"
	}
	return []model.ToolHealth{{
		// A live incident is one active event, not an accumulated session count.
		Tool: fields["Tool"], WorstSeverity: fields["Severity"], SessionCount: 1,
		LastOccurrence: started, Pain: []string{fields["Status"]}, Kind: "live_incident",
	}}, false, ""
}

func parseAccumulated(text string) ([]model.ToolHealth, bool, string) {
	if !strings.Contains(text, "Tool | Severity | Sessions | Last seen | Pain") || !strings.Contains(text, "Kind: accumulated_friction") {
		return nil, false, "contract_drift"
	}
	rows := make([]model.ToolHealth, 0)
	partial := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") || strings.Contains(line, "| Tool |") || strings.Contains(line, "---") {
			continue
		}
		parts := strings.Split(strings.Trim(line, "|"), "|")
		if len(parts) != 5 {
			partial = true
			continue
		}
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		sessions, countErr := strconv.Atoi(parts[2])
		last, timeErr := time.Parse(time.RFC3339Nano, parts[3])
		if parts[0] == "" || parts[1] == "" || parts[4] == "" || countErr != nil || sessions < 1 || timeErr != nil {
			partial = true
			continue
		}
		rows = append(rows, model.ToolHealth{Tool: parts[0], WorstSeverity: parts[1], SessionCount: sessions, LastOccurrence: last, Pain: []string{parts[4]}, Kind: "accumulated_friction"})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Tool < rows[j].Tool })
	return rows, partial, ""
}
