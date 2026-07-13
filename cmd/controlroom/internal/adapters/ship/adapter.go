// Package ship adapts Ship's frozen workflow and driver JSON contracts.
package ship

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

const defaultTimeout = 20 * time.Second

var (
	idRemainderPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	sensitiveFailure   = regexp.MustCompile(`(?i)([a-z]:\\|(?:^|\W)(?:token|password|secret|api[_-]?key|key)\s*=|bearer\s+|authorization\s*:|[a-z][a-z0-9+.-]*://[^/\s:@]+:[^@\s/]+@|-----begin [^-]*private key-----)`)
)

// Result is Ship's source-local collection result.
type Result struct {
	Runs    []model.Run
	Receipt model.SourceReceipt
}

type runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, executable string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, executable, args...).Output()
}

// Adapter executes only Ship's read-only list and status verbs.
type Adapter struct {
	executable string
	timeout    time.Duration
	runner     runner
	now        func() time.Time
}

// New constructs a Ship adapter.
func New(executable string) *Adapter {
	return &Adapter{executable: executable, timeout: defaultTimeout, runner: commandRunner{}, now: time.Now}
}

// Collect reads workflow and driver inventory under one source deadline.
func (a *Adapter) Collect(ctx context.Context) Result {
	started := a.now()
	receipt := model.SourceReceipt{Source: "ship", ObservedAt: started}
	finish := func(state model.SourceState, code, message string, runs []model.Run) Result {
		receipt.State, receipt.ErrorCode, receipt.Message = state, code, message
		receipt.DurationMS = max(0, a.now().Sub(started).Milliseconds())
		sort.Slice(runs, func(i, j int) bool {
			if !runs[i].UpdatedAt.Equal(runs[j].UpdatedAt) {
				return runs[i].UpdatedAt.After(runs[j].UpdatedAt)
			}
			return runs[i].ID < runs[j].ID
		})
		return Result{Runs: runs, Receipt: receipt}
	}
	if a.executable == "" {
		return finish(model.SourceUnavailable, "not_configured", "Ship is not configured", nil)
	}

	sourceCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	workflowJSON, err := a.runner.Run(sourceCtx, a.executable, "list", "--json")
	if err != nil {
		return finish(commandState(sourceCtx, err), commandCode(sourceCtx, err), "Ship workflow inventory is unavailable", nil)
	}
	var workflowList struct {
		Runs []workflowWire `json:"runs"`
	}
	// The frozen Ship contract requires `runs` to be an array; null is
	// malformed rather than an empty inventory so undercounting fails closed.
	if err := json.Unmarshal(workflowJSON, &workflowList); err != nil || workflowList.Runs == nil {
		return finish(model.SourceUnavailable, "malformed_inventory", "Ship returned malformed workflow inventory", nil)
	}

	driverJSON, err := a.runner.Run(sourceCtx, a.executable, "driver", "list", "--json")
	if err != nil {
		return finish(commandState(sourceCtx, err), commandCode(sourceCtx, err), "Ship driver inventory is unavailable", nil)
	}
	var driverList struct {
		Runs []driverWire `json:"runs"`
	}
	// Driver inventory follows the same required-array contract.
	if err := json.Unmarshal(driverJSON, &driverList); err != nil || driverList.Runs == nil {
		return finish(model.SourceUnavailable, "malformed_inventory", "Ship returned malformed driver inventory", nil)
	}

	workflows, workflowDegraded := a.collectWorkflows(sourceCtx, workflowList.Runs)
	drivers, driverDegraded := a.collectDrivers(sourceCtx, driverList.Runs)
	runs := append(workflows, drivers...)
	degraded := workflowDegraded || driverDegraded
	if degraded {
		return finish(model.SourceDegraded, "partial_detail", "some Ship rows were incomplete", runs)
	}
	return finish(model.SourceOK, "", "", runs)
}

func (a *Adapter) collectWorkflows(ctx context.Context, inventory []workflowWire) ([]model.Run, bool) {
	runs := make([]model.Run, 0, len(inventory))
	degraded := false
	for _, wire := range inventory {
		if !validID(wire.ID, "wf_") {
			degraded = true
			continue
		}
		if needsWorkflowDetail(wire) {
			detailJSON, detailErr := a.runner.Run(ctx, a.executable, "status", wire.ID, "--json")
			var detail workflowWire
			if detailErr != nil || json.Unmarshal(detailJSON, &detail) != nil || detail.ID != wire.ID {
				degraded = true
			} else {
				wire = detail
			}
		}
		runs = append(runs, normalizeWorkflow(wire))
	}
	return runs, degraded
}

func (a *Adapter) collectDrivers(ctx context.Context, inventory []driverWire) ([]model.Run, bool) {
	runs := make([]model.Run, 0, len(inventory))
	degraded := false
	for _, wire := range inventory {
		if !validID(wire.ID, "drv_") {
			degraded = true
			continue
		}
		if needsDriverDetail(wire) {
			detailJSON, detailErr := a.runner.Run(ctx, a.executable, "driver", "status", wire.ID, "--json")
			var detail driverWire
			if detailErr != nil || json.Unmarshal(detailJSON, &detail) != nil || detail.ID != wire.ID {
				degraded = true
			} else {
				wire = detail
			}
		}
		runs = append(runs, normalizeDriver(wire)...)
	}
	return runs, degraded
}

type workflowWire struct {
	ID              string         `json:"id"`
	Repo            string         `json:"repo"`
	DocPath         string         `json:"docPath"`
	Status          string         `json:"status"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
	EndedAt         *time.Time     `json:"endedAt"`
	FailureCategory string         `json:"failureCategory"`
	ErrorMessage    string         `json:"errorMessage"`
	Worktree        *worktreeWire  `json:"worktree"`
	Phases          []phaseWire    `json:"phases"`
	Observability   *observability `json:"observability"`
}

type worktreeWire struct {
	Branch string `json:"branch"`
}

type phaseWire struct {
	Kind      string     `json:"kind"`
	Status    string     `json:"status"`
	StartedAt *time.Time `json:"startedAt"`
	EndedAt   *time.Time `json:"endedAt"`
}

type observability struct {
	Requested  runtimeWire   `json:"requested"`
	Actual     runtimeWire   `json:"actual"`
	StartedAt  *time.Time    `json:"startedAt"`
	EndedAt    *time.Time    `json:"endedAt"`
	DurationMS *int64        `json:"durationMs"`
	Evidence   *evidenceWire `json:"evidence"`
}

type runtimeWire struct {
	Runtime  string    `json:"runtime"`
	Provider string    `json:"provider"`
	Model    modelWire `json:"model"`
}

type modelWire struct {
	ID string `json:"id"`
}

type evidenceWire struct {
	Availability string `json:"availability"`
	Refs         []struct {
		Path string `json:"path"`
	} `json:"refs"`
}

type driverWire struct {
	ID          string        `json:"driverRunId"`
	Status      string        `json:"status"`
	Repo        string        `json:"repo"`
	Project     string        `json:"project"`
	Phase       string        `json:"phase"`
	CreatedAt   time.Time     `json:"createdAt"`
	UpdatedAt   time.Time     `json:"updatedAt"`
	ManifestRef string        `json:"manifestRef"`
	Batches     []driverBatch `json:"batches"`
}

type driverBatch struct {
	Streams []driverStream `json:"streams"`
}

type driverStream struct {
	ID            string          `json:"streamId"`
	TaskID        string          `json:"taskId"`
	TaskSlug      string          `json:"taskSlug"`
	SpecPath      string          `json:"specPath"`
	Branch        string          `json:"branch"`
	Runtime       string          `json:"runtime"`
	Provider      string          `json:"provider"`
	ModelTier     string          `json:"modelTier"`
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
	ErrorMessage  string          `json:"errorMessage"`
	WorkflowRunID string          `json:"workflowRunId"`
	Attempts      []driverAttempt `json:"attempts"`
}

type driverAttempt struct {
	DispatchedAt time.Time  `json:"dispatchedAt"`
	Terminal     bool       `json:"terminal"`
	EndedAt      *time.Time `json:"endedAt"`
}

func needsWorkflowDetail(w workflowWire) bool {
	if w.Status == "" || w.CreatedAt.IsZero() || w.UpdatedAt.IsZero() || w.Phases == nil || w.Observability == nil {
		return true
	}
	o := w.Observability
	if o.Requested.Runtime == "" || o.Requested.Provider == "" || o.Requested.Model.ID == "" || o.Actual.Runtime == "" || o.Actual.Provider == "" || o.Actual.Model.ID == "" || o.StartedAt == nil || o.DurationMS == nil || o.Evidence == nil || o.Evidence.Availability == "" {
		return true
	}
	return w.Status == "failed" && w.FailureCategory == "" && w.ErrorMessage == ""
}

func needsDriverDetail(w driverWire) bool {
	if w.Status == "" || w.Repo == "" || w.Project == "" || w.Phase == "" || w.CreatedAt.IsZero() || w.UpdatedAt.IsZero() || w.Batches == nil {
		return true
	}
	for _, batch := range w.Batches {
		if batch.Streams == nil {
			return true
		}
		for _, stream := range batch.Streams {
			if stream.ID == "" || stream.Status == "" || stream.CreatedAt.IsZero() || stream.UpdatedAt.IsZero() || stream.Attempts == nil {
				return true
			}
		}
	}
	return false
}

func normalizeWorkflow(w workflowWire) model.Run {
	run := model.Run{
		ID: w.ID, Kind: "workflow", Repository: w.Repo, Status: w.Status,
		DocPath: availabilityPath(w.DocPath), SpecPath: model.Missing[string](), CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
		StartedAt: model.Missing[time.Time](), EndedAt: availabilityTime(w.EndedAt), DurationMS: model.Missing[int64](),
		Requested: missingRuntime(), Actual: missingRuntime(), Evidence: nil, Liveness: model.LivenessUnknown,
	}
	if w.Worktree != nil {
		run.Branch = w.Worktree.Branch
	}
	if len(w.Phases) > 0 {
		run.Phase = w.Phases[len(w.Phases)-1].Kind
	}
	if w.Observability != nil {
		o := w.Observability
		run.Requested, run.Actual = normalizeRuntime(o.Requested), normalizeRuntime(o.Actual)
		run.StartedAt, run.EndedAt, run.DurationMS = availabilityTime(o.StartedAt), availabilityTime(o.EndedAt), availabilityInt(o.DurationMS)
		if o.Evidence != nil && o.Evidence.Availability == string(model.Available) {
			// Non-nil preserves the explicit owner availability fact for the
			// Tracelens eligibility gate without exposing prompt or raw-trace refs.
			run.Evidence = make([]model.SafeLink, 0)
		}
		if o.EndedAt == nil {
			run.EndedAt = availabilityTime(w.EndedAt)
		}
	}
	run.Failure = sanitizeFailure(firstNonempty(w.FailureCategory, w.ErrorMessage))
	return run
}

func normalizeDriver(w driverWire) []model.Run {
	rows := []model.Run{{
		ID: w.ID, Kind: "driver", Repository: w.Repo, Project: w.Project, Status: w.Status, Phase: w.Phase,
		DocPath: model.Missing[string](), SpecPath: availabilityPath(w.ManifestRef), CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt,
		StartedAt: model.Missing[time.Time](), EndedAt: model.Missing[time.Time](), DurationMS: model.Missing[int64](),
		Requested: missingRuntime(), Actual: missingRuntime(), Evidence: []model.SafeLink{}, Liveness: model.LivenessUnknown,
	}}
	for _, batch := range w.Batches {
		for _, stream := range batch.Streams {
			requested := missingRuntime()
			requested.Runtime, requested.Provider, requested.Model = availabilityString(stream.Runtime), availabilityString(stream.Provider), availabilityString(stream.ModelTier)
			startedAt, endedAt, duration := attemptTimes(stream.Attempts)
			rows = append(rows, model.Run{
				ID: stream.ID, Kind: "driver", Repository: w.Repo, Project: w.Project, Task: firstNonempty(stream.TaskSlug, stream.TaskID),
				SpecPath: availabilityPath(stream.SpecPath), DocPath: model.Missing[string](), Branch: stream.Branch, Status: stream.Status, Phase: w.Phase,
				Requested: requested, Actual: missingRuntime(), CreatedAt: stream.CreatedAt, UpdatedAt: stream.UpdatedAt,
				StartedAt: startedAt, EndedAt: endedAt, DurationMS: duration,
				Failure: sanitizeFailure(stream.ErrorMessage), Evidence: []model.SafeLink{}, Liveness: model.LivenessUnknown,
			})
		}
	}
	return rows
}

func validID(id, prefix string) bool {
	return strings.HasPrefix(id, prefix) && idRemainderPattern.MatchString(strings.TrimPrefix(id, prefix))
}

func attemptTimes(attempts []driverAttempt) (model.Availability[time.Time], model.Availability[time.Time], model.Availability[int64]) {
	var started, ended *time.Time
	for i := range attempts {
		attempt := &attempts[i]
		if !attempt.DispatchedAt.IsZero() && (started == nil || attempt.DispatchedAt.Before(*started)) {
			value := attempt.DispatchedAt
			started = &value
		}
		if attempt.Terminal && attempt.EndedAt != nil && (ended == nil || attempt.EndedAt.After(*ended)) {
			value := *attempt.EndedAt
			ended = &value
		}
	}
	duration := model.Missing[int64]()
	if started != nil && ended != nil && !ended.Before(*started) {
		duration = model.Known(ended.Sub(*started).Milliseconds())
	}
	return availabilityTime(started), availabilityTime(ended), duration
}

func availabilityPath(value string) model.Availability[string] {
	value = strings.TrimSpace(value)
	clean := path.Clean(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.Contains(value, "\\") || strings.Contains(value, ":") || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || clean != value {
		return model.Missing[string]()
	}
	return model.Known(value)
}

func availabilityString(value string) model.Availability[string] {
	if strings.TrimSpace(value) == "" {
		return model.Missing[string]()
	}
	return model.Known(strings.TrimSpace(value))
}

func availabilityTime(value *time.Time) model.Availability[time.Time] {
	if value == nil || value.IsZero() {
		return model.Missing[time.Time]()
	}
	return model.Known(*value)
}

func availabilityInt(value *int64) model.Availability[int64] {
	if value == nil {
		return model.Missing[int64]()
	}
	return model.Known(*value)
}

func normalizeRuntime(w runtimeWire) model.RuntimeDetails {
	return model.RuntimeDetails{Runtime: availabilityString(w.Runtime), Provider: availabilityString(w.Provider), Model: availabilityString(w.Model.ID)}
}

func missingRuntime() model.RuntimeDetails {
	return model.RuntimeDetails{Runtime: model.Missing[string](), Provider: model.Missing[string](), Model: model.Missing[string]()}
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func sanitizeFailure(value string) string {
	value = strings.TrimSpace(value)
	if sensitiveFailure.MatchString(value) {
		return "failure detail redacted"
	}
	if len(value) > 300 {
		return value[:300]
	}
	return value
}

func commandState(_ context.Context, _ error) model.SourceState {
	return model.SourceUnavailable
}

func commandCode(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return "executable_not_found"
	}
	return "command_failed"
}
