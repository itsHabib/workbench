// Package tracelens adapts bounded on-demand Ship trace diagnoses.
package tracelens

import (
	"context"
	"encoding/json"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

const (
	defaultTimeout = 10 * time.Second
	eligibilityAge = 14 * 24 * time.Hour
	maxRuns        = 5
)

var sensitiveText = regexp.MustCompile(`(?i)([a-z]:\\|/(?:users|home)/|token\s*[=:]|password\s*[=:]|authorization\s*:)`)

// Result is Tracelens's source-local collection result.
type Result struct {
	Diagnoses []model.Diagnosis
	Receipt   model.SourceReceipt
}

type runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, executable string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, executable, args...).Output()
}

// Adapter executes the stateless Tracelens owner contract.
type Adapter struct {
	executable string
	timeout    time.Duration
	runner     runner
	now        func() time.Time
}

// New constructs a Tracelens adapter.
func New(executable string) *Adapter {
	return &Adapter{executable: executable, timeout: defaultTimeout, runner: commandRunner{}, now: time.Now}
}

// Collect diagnoses at most five eligible workflow runs. A non-nil Run.Evidence
// slice is the source-local preservation of Ship's explicit evidence-available
// fact; the links themselves may be empty and are never passed to Tracelens.
func (a *Adapter) Collect(ctx context.Context, runs []model.Run, shipReceipt model.SourceReceipt) Result {
	started := a.now()
	receipt := model.SourceReceipt{Source: "tracelens", ObservedAt: started}
	finish := func(state model.SourceState, code, message string, rows []model.Diagnosis) Result {
		receipt.State, receipt.ErrorCode, receipt.Message = state, code, message
		receipt.DurationMS = max(0, a.now().Sub(started).Milliseconds())
		return Result{Diagnoses: rows, Receipt: receipt}
	}
	if shipReceipt.State != model.SourceOK {
		return finish(model.SourceUnavailable, "ship_not_current", "Ship evidence is not current", nil)
	}
	if a.executable == "" {
		return finish(model.SourceUnavailable, "not_configured", "Tracelens is not configured", nil)
	}

	eligible := selectEligible(runs, started)
	rows := make([]model.Diagnosis, 0, len(eligible))
	failures := 0
	for _, run := range eligible {
		callCtx, cancel := context.WithTimeout(ctx, a.timeout)
		stdout, err := a.runner.Run(callCtx, a.executable, "ship", "-json", run.ID)
		callErr := callCtx.Err()
		cancel()
		if err != nil || callErr != nil {
			failures++
			continue
		}
		var diagnosis model.Diagnosis
		if err := json.Unmarshal(stdout, &diagnosis); err != nil || diagnosis.RunID == "" || diagnosis.RunID != run.ID {
			failures++
			continue
		}
		if !safeDiagnosis(&diagnosis) {
			failures++
			continue
		}
		rows = append(rows, diagnosis)
	}
	if failures > 0 {
		return finish(model.SourceDegraded, "partial_analysis", "one or more Tracelens analyses were unavailable", rows)
	}
	return finish(model.SourceOK, "", "", rows)
}

func selectEligible(runs []model.Run, now time.Time) []model.Run {
	terminal := map[string]bool{"succeeded": true, "failed": true, "cancelled": true, "canceled": true, "timed_out": true}
	selected := make([]model.Run, 0, min(len(runs), maxRuns))
	cutoff := now.Add(-eligibilityAge)
	for _, run := range runs {
		if run.Kind != "workflow" || !terminal[run.Status] || run.ID == "" || run.Evidence == nil || run.UpdatedAt.Before(cutoff) || run.UpdatedAt.After(now) {
			continue
		}
		selected = append(selected, run)
	}
	sort.Slice(selected, func(i, j int) bool {
		if !selected[i].UpdatedAt.Equal(selected[j].UpdatedAt) {
			return selected[i].UpdatedAt.After(selected[j].UpdatedAt)
		}
		return selected[i].ID < selected[j].ID
	})
	if len(selected) > maxRuns {
		selected = selected[:maxRuns]
	}
	return selected
}

func safeDiagnosis(d *model.Diagnosis) bool {
	if d.Report.State == model.Available {
		if d.Report.Value == nil || d.Report.Value.Validate() != nil {
			return false
		}
	}
	for _, link := range d.Evidence {
		if link.Validate() != nil {
			return false
		}
	}
	for i := range d.Findings {
		finding := &d.Findings[i]
		finding.Title = sanitize(finding.Title)
		finding.Severity = sanitize(finding.Severity)
		finding.Locus = sanitize(finding.Locus)
		finding.Evidence = sanitize(finding.Evidence)
		finding.Repair = sanitize(finding.Repair)
	}
	return d.InputTokens.Validate() == nil && d.OutputTokens.Validate() == nil && d.CostUSD.Validate() == nil && d.LatencyMS.Validate() == nil
}

func sanitize(value string) string {
	value = strings.TrimSpace(value)
	if sensitiveText.MatchString(value) {
		return "[redacted]"
	}
	if len(value) > 500 {
		return value[:500]
	}
	return value
}
