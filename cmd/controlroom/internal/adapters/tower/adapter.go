// Package tower adapts Tower's optional read-only worktree inventory.
package tower

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"sort"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

const defaultTimeout = 10 * time.Second

// Worktree is supplemental Tower context. Path is deliberately opaque and is
// not safe to expose or open until the composition layer validates it.
type Worktree struct {
	Repository string    `json:"repository"`
	Branch     string    `json:"branch"`
	Path       string    `json:"path"`
	Head       string    `json:"head,omitempty"`
	Title      string    `json:"title,omitempty"`
	Dirty      bool      `json:"dirty"`
	Ahead      int       `json:"ahead"`
	Behind     int       `json:"behind"`
	LastCommit time.Time `json:"last_commit,omitempty"`
}

// Result is Tower's source-local collection result.
type Result struct {
	Worktrees []Worktree
	Receipt   model.SourceReceipt
}

type runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, executable string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, executable, args...).Output()
}

// Adapter executes the immutable, non-reconciling Tower contract.
type Adapter struct {
	executable string
	timeout    time.Duration
	runner     runner
	now        func() time.Time
}

// New constructs a Tower adapter. An empty executable makes Tower normally
// unavailable; Tower is an optional source.
func New(executable string) *Adapter {
	return &Adapter{executable: executable, timeout: defaultTimeout, runner: commandRunner{}, now: time.Now}
}

// Collect runs exactly `tower ls --json --no-reconcile`.
func (a *Adapter) Collect(ctx context.Context) Result {
	started := a.now()
	receipt := model.SourceReceipt{Source: "tower", ObservedAt: started}
	finish := func(state model.SourceState, code, message string) Result {
		receipt.State, receipt.ErrorCode, receipt.Message = state, code, message
		receipt.DurationMS = max(0, a.now().Sub(started).Milliseconds())
		return Result{Receipt: receipt}
	}
	if a.executable == "" {
		return finish(model.SourceUnavailable, "not_configured", "Tower is not configured")
	}

	callCtx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()
	stdout, err := a.runner.Run(callCtx, a.executable, "ls", "--json", "--no-reconcile")
	if err != nil {
		if errors.Is(callCtx.Err(), context.DeadlineExceeded) {
			return finish(model.SourceUnavailable, "timeout", "Tower collection timed out")
		}
		var execErr *exec.Error
		if errors.As(err, &execErr) {
			return finish(model.SourceUnavailable, "executable_not_found", "Tower executable is unavailable")
		}
		return finish(model.SourceUnavailable, "command_failed", "Tower collection failed")
	}

	var wire []struct {
		Worktree struct {
			Repo       string    `json:"repo"`
			Branch     string    `json:"branch"`
			Path       string    `json:"path"`
			Head       string    `json:"head"`
			Title      string    `json:"title"`
			Dirty      bool      `json:"dirty"`
			Ahead      int       `json:"ahead"`
			Behind     int       `json:"behind"`
			LastCommit time.Time `json:"last_commit"`
		} `json:"worktree"`
	}
	if err := json.Unmarshal(stdout, &wire); err != nil {
		return finish(model.SourceUnavailable, "malformed_output", "Tower returned malformed JSON")
	}

	rows := make([]Worktree, 0, len(wire))
	seen := make(map[string]struct{}, len(wire))
	for _, item := range wire {
		w := item.Worktree
		if w.Repo == "" || w.Branch == "" {
			return finish(model.SourceUnavailable, "ambiguous_identity", "Tower returned an incomplete worktree identity")
		}
		key := w.Repo + "\x00" + w.Branch
		if _, ok := seen[key]; ok {
			return finish(model.SourceUnavailable, "duplicate_identity", "Tower returned a duplicate worktree identity")
		}
		seen[key] = struct{}{}
		rows = append(rows, Worktree{Repository: w.Repo, Branch: w.Branch, Path: w.Path, Head: w.Head, Title: w.Title, Dirty: w.Dirty, Ahead: w.Ahead, Behind: w.Behind, LastCommit: w.LastCommit})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Repository != rows[j].Repository {
			return rows[i].Repository < rows[j].Repository
		}
		if rows[i].Branch != rows[j].Branch {
			return rows[i].Branch < rows[j].Branch
		}
		return rows[i].Path < rows[j].Path
	})
	receipt.State = model.SourceOK
	receipt.DurationMS = max(0, a.now().Sub(started).Milliseconds())
	return Result{Worktrees: rows, Receipt: receipt}
}
