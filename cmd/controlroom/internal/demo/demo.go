// Package demo provides the deterministic golden scenario for Control Room.
package demo

import (
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
	"github.com/itsHabib/workbench/cmd/controlroom/internal/policy"
)

// Clock is the fixed instant used to make the demo scenario reproducible.
var Clock = time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)

// Snapshot returns the policy-applied Control Room demo story.
func Snapshot() model.Snapshot {
	doc := model.Known("docs/features/example-feature/spec.md")
	unknown := model.Missing[string]()
	successChecks := []model.Check{{Name: "ci", Status: "COMPLETED", Conclusion: "SUCCESS"}}
	snapshot := model.Snapshot{
		Version:     1,
		Mode:        "demo",
		GeneratedAt: Clock,
		Sources: []model.SourceReceipt{
			{Source: "ship", State: model.SourceOK, ObservedAt: Clock, DurationMS: 40},
			{Source: "dossier", State: model.SourceOK, ObservedAt: Clock, DurationMS: 25},
			{Source: "github", State: model.SourceOK, ObservedAt: Clock, DurationMS: 80},
			{Source: "tracelens", State: model.SourceDegraded, ObservedAt: Clock, DurationMS: 100},
			{Source: "toolhealth", State: model.SourceStale, ObservedAt: Clock.Add(-2 * time.Hour), DurationMS: 10},
			{Source: "tower", State: model.SourceUnavailable, ObservedAt: Clock, DurationMS: 5, ErrorCode: "executable_not_found", Message: "tower executable not found"},
		},
		Runs: []model.Run{
			{ID: "wf_demo_live", Kind: "workflow", Repository: "example-repo", Status: "succeeded", DocPath: model.Known("docs/live/spec.md"), SpecPath: unknown, UpdatedAt: Clock.Add(-time.Hour), CreatedAt: Clock.Add(-2 * time.Hour), Evidence: []model.SafeLink{}},
			{ID: "drv_demo_waiting", Kind: "driver", Repository: "example-repo", Project: "workbench", Status: "running", Phase: "awaiting_judgment", SpecPath: model.Known("docs/waiting/spec.md"), DocPath: unknown, UpdatedAt: Clock.Add(-time.Hour), CreatedAt: Clock.Add(-2 * time.Hour), Evidence: []model.SafeLink{}},
			{ID: "wf_demo_stalled", Kind: "workflow", Repository: "example-repo", Status: "running", DocPath: model.Known("docs/stalled/spec.md"), SpecPath: unknown, UpdatedAt: Clock.Add(-20 * time.Minute), CreatedAt: Clock.Add(-time.Hour), Evidence: []model.SafeLink{}},
			{ID: "wf_demo_fail_1", Kind: "workflow", Repository: "example-repo", Status: "failed", DocPath: doc, SpecPath: unknown, UpdatedAt: Clock.Add(-48 * time.Hour), CreatedAt: Clock.Add(-49 * time.Hour), Failure: "test failure", Evidence: []model.SafeLink{}},
			{ID: "wf_demo_fail_2", Kind: "workflow", Repository: "example-repo", Status: "failed", DocPath: doc, SpecPath: unknown, UpdatedAt: Clock.Add(-24 * time.Hour), CreatedAt: Clock.Add(-25 * time.Hour), Failure: "test failure", Evidence: []model.SafeLink{}},
			{ID: "wf_demo_fail_3", Kind: "workflow", Repository: "example-repo", Status: "failed", DocPath: doc, SpecPath: unknown, UpdatedAt: Clock.Add(-2 * time.Hour), CreatedAt: Clock.Add(-3 * time.Hour), Failure: "test failure", Evidence: []model.SafeLink{}},
		},
		Tasks: []model.Task{
			{ID: "tsk_done", Slug: "done-dependency", Title: "Done dependency", Project: "workbench", Status: "done", CreatedAt: Clock.Add(-48 * time.Hour), UpdatedAt: Clock.Add(-24 * time.Hour), Dependencies: []string{}, Blockers: []string{}, Artifacts: []model.SafeLink{}},
			{ID: "tsk_blocked", Slug: "blocked-no-path", Title: "Blocked without a path", Project: "workbench", Status: "blocked", CreatedAt: Clock.Add(-48 * time.Hour), UpdatedAt: Clock.Add(-4 * time.Hour), Dependencies: []string{}, Blockers: []string{}, Artifacts: []model.SafeLink{}},
			{ID: "tsk_ready", Slug: "ready-task", Title: "Ready task", Project: "workbench", Status: "todo", CreatedAt: Clock.Add(-4 * time.Hour), UpdatedAt: Clock.Add(-time.Hour), Dependencies: []string{"tsk_done"}, Blockers: []string{}, Artifacts: []model.SafeLink{}},
		},
		PullRequests: []model.PullRequest{
			{ID: "example-repo#42", Repository: "example-repo", Number: 42, Title: "Failed CI", URL: "https://example.invalid/pr/42", State: "open", UpdatedAt: Clock.Add(-30 * time.Minute), CreatedAt: Clock.Add(-4 * time.Hour), Checks: []model.Check{{Name: "ci", Status: "COMPLETED", Conclusion: "FAILURE"}}, ReviewDecision: "REVIEW_REQUIRED", DetailState: "complete", Mergeable: "MERGEABLE", MergeStateStatus: "BLOCKED"},
			{ID: "example-repo#43", Repository: "example-repo", Number: 43, Title: "Needs review", URL: "https://example.invalid/pr/43", State: "open", UpdatedAt: Clock.Add(-20 * time.Minute), CreatedAt: Clock.Add(-3 * time.Hour), Checks: successChecks, ReviewDecision: "REVIEW_REQUIRED", RequestedReviewers: 1, DetailState: "complete", Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN"},
		},
		Reliability: []model.Diagnosis{{
			RunID: "wf_demo_fail_3", Verdict: "escalate", Tier: "T2", Dialect: "ship-trace-v1",
			Findings: []model.Finding{{Title: "Retry loop detected", Severity: "high", Locus: "phase:implement", Evidence: "three related failures"}},
			Report:   model.Known(model.SafeLink{Label: "report", URL: "https://example.invalid/report"}), Evidence: []model.SafeLink{},
			InputTokens: model.Missing[int64](), OutputTokens: model.Missing[int64](), CostUSD: model.NotAvailable[float64](), LatencyMS: model.NotAvailable[int64](),
		}},
		ToolHealth:   []model.ToolHealth{{Tool: "ship", WorstSeverity: "P2", SessionCount: 3, LastOccurrence: Clock.Add(-48 * time.Hour), Pain: []string{"cloud dispatch needs credential"}, Kind: "accumulated_friction", Stale: true}},
		Repositories: []string{"example-repo"},
	}
	return policy.ApplyPolicy(snapshot, Clock)
}
