package policy_test

import (
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
	"github.com/itsHabib/workbench/cmd/controlroom/internal/policy"
)

var now = time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)

func receipt(source string, state model.SourceState) model.SourceReceipt {
	return model.SourceReceipt{Source: source, State: state, ObservedAt: now}
}

func base() model.Snapshot {
	return model.Snapshot{GeneratedAt: now, Sources: []model.SourceReceipt{
		receipt("ship", model.SourceOK), receipt("dossier", model.SourceOK), receipt("github", model.SourceOK),
		receipt("tracelens", model.SourceOK), receipt("toolhealth", model.SourceOK),
	}}
}

func ruleIDs(snapshot model.Snapshot) []string {
	ids := make([]string, len(snapshot.Attention))
	for i, item := range snapshot.Attention {
		ids[i] = item.RuleID
	}
	return ids
}

func hasRule(snapshot model.Snapshot, rule string) bool {
	for _, item := range snapshot.Attention {
		if item.RuleID == rule {
			return true
		}
	}
	return false
}

func TestRunThresholdsAndRetryGrouping(t *testing.T) {
	doc := model.Known("docs/example/spec.md")
	unknown := model.Missing[string]()
	snapshot := base()
	snapshot.Runs = []model.Run{
		{ID: "stalled", Kind: "workflow", Status: "running", UpdatedAt: now.Add(-15 * time.Minute), DocPath: model.Known("docs/stalled.md"), SpecPath: unknown},
		{ID: "f1", Kind: "workflow", Status: "failed", UpdatedAt: now.Add(-72 * time.Hour), DocPath: doc, SpecPath: unknown},
		{ID: "f2", Kind: "workflow", Status: "failed", UpdatedAt: now.Add(-2 * time.Hour), DocPath: doc, SpecPath: unknown},
		{ID: "f3", Kind: "workflow", Status: "failed", UpdatedAt: now.Add(-time.Hour), DocPath: doc, SpecPath: unknown},
	}
	got := policy.ApplyPolicy(snapshot, now)
	if got.Runs[0].Liveness != model.LivenessStalledActive || !hasRule(got, "run.stalled_active") {
		t.Fatal("15m active run must be stalled")
	}
	for _, run := range got.Runs[1:] {
		if run.Liveness != model.LivenessRetryLoop {
			t.Fatalf("%s liveness = %s", run.ID, run.Liveness)
		}
	}
	if got.Attention[0].RuleID != "run.retry_loop" || got.Attention[0].Score != 100 {
		t.Fatalf("top attention = %+v", got.Attention[0])
	}
}

func TestApplyPolicyDoesNotMutateCallerSlices(t *testing.T) {
	snapshot := base()
	snapshot.Runs = []model.Run{{ID: "run", Kind: "workflow", Status: "running", UpdatedAt: now, DocPath: model.Known("docs/a.md")}}
	snapshot.Tasks = []model.Task{{ID: "task", Status: "done", UpdatedAt: now}}
	got := policy.ApplyPolicy(snapshot, now)
	if snapshot.Runs[0].Liveness != "" || snapshot.Tasks[0].Liveness != "" || snapshot.Attention != nil {
		t.Fatalf("input mutated: %+v", snapshot)
	}
	if got.Runs[0].Liveness != model.LivenessLive || got.Tasks[0].Liveness != model.LivenessDone {
		t.Fatalf("derived output missing: %+v", got)
	}
}

func TestLivenessFallbacksAnd336HourBoundary(t *testing.T) {
	snapshot := base()
	snapshot.Runs = []model.Run{
		{ID: "idle", Kind: "workflow", Status: "failed", UpdatedAt: now.Add(-336 * time.Hour), DocPath: model.Known("docs/idle.md")},
		{ID: "old", Kind: "workflow", Status: "failed", UpdatedAt: now.Add(-337 * time.Hour), DocPath: model.Known("docs/old.md")},
		{ID: "terminal", Kind: "workflow", Status: "cancelled", UpdatedAt: time.Time{}, DocPath: model.Missing[string]()},
	}
	got := policy.ApplyPolicy(snapshot, now)
	want := []model.Liveness{model.LivenessIdle, model.LivenessUnknown, model.LivenessUnknown}
	for i := range want {
		if got.Runs[i].Liveness != want[i] {
			t.Fatalf("run %s liveness = %s want %s", got.Runs[i].ID, got.Runs[i].Liveness, want[i])
		}
	}
}

func TestMissingTimestampsDoNotTriggerTimeBasedLabels(t *testing.T) {
	snapshot := base()
	snapshot.Runs = []model.Run{{ID: "active", Kind: "workflow", Status: "running", UpdatedAt: time.Time{}, DocPath: model.Known("docs/active.md")}}
	snapshot.Tasks = []model.Task{{ID: "claimed", Slug: "claimed", Status: "claimed", UpdatedAt: time.Time{}}}
	got := policy.ApplyPolicy(snapshot, now)
	if got.Runs[0].Liveness != model.LivenessLive {
		t.Fatalf("run liveness = %s want %s", got.Runs[0].Liveness, model.LivenessLive)
	}
	if got.Tasks[0].Liveness != model.LivenessUnknown {
		t.Fatalf("task liveness = %s want %s", got.Tasks[0].Liveness, model.LivenessUnknown)
	}
	if hasRule(got, "run.stalled_active") || hasRule(got, "task.stale_claim") {
		t.Fatalf("missing timestamps emitted time-based rules: %v", ruleIDs(got))
	}
}

func TestWorkflowAndDriverRetryGroupsNeverMix(t *testing.T) {
	path := "docs/example/spec.md"
	snapshot := base()
	snapshot.Runs = []model.Run{
		{ID: "w1", Kind: "workflow", Status: "failed", UpdatedAt: now, DocPath: model.Known(path)},
		{ID: "w2", Kind: "workflow", Status: "failed", UpdatedAt: now, DocPath: model.Known(path)},
		{ID: "d1", Kind: "driver", Status: "failed", UpdatedAt: now, SpecPath: model.Known(path)},
	}
	got := policy.ApplyPolicy(snapshot, now)
	if hasRule(got, "run.retry_loop") {
		t.Fatal("workflow and driver failures combined")
	}
}

func TestRetryGroupsRequireExactAvailableIdentity(t *testing.T) {
	snapshot := base()
	snapshot.Runs = []model.Run{
		{ID: "a1", Kind: "workflow", Status: "failed", UpdatedAt: now, DocPath: model.Known("docs/a.md")},
		{ID: "a2", Kind: "workflow", Status: "failed", UpdatedAt: now, DocPath: model.Known("docs/a.md")},
		{ID: "b", Kind: "workflow", Status: "failed", UpdatedAt: now, DocPath: model.Known("docs/b.md")},
		{ID: "unknown", Kind: "workflow", Status: "failed", UpdatedAt: now, DocPath: model.Missing[string]()},
	}
	if got := policy.ApplyPolicy(snapshot, now); hasRule(got, "run.retry_loop") {
		t.Fatal("different or unavailable identities combined")
	}
}

func TestTaskBoundariesAndFailClosedStaleClaim(t *testing.T) {
	snapshot := base()
	snapshot.Tasks = []model.Task{
		{ID: "done", Slug: "done", Status: "done", UpdatedAt: now.Add(-time.Hour)},
		{ID: "idle", Slug: "idle", Status: "todo", UpdatedAt: now.Add(-72 * time.Hour)},
		{ID: "stale", Slug: "stale", Status: "claimed", UpdatedAt: now.Add(-337 * time.Hour)},
		{ID: "blocked", Slug: "blocked", Status: "blocked", UpdatedAt: now, Dependencies: []string{"missing"}},
	}
	got := policy.ApplyPolicy(snapshot, now)
	want := []model.Liveness{model.LivenessDone, model.LivenessLive, model.LivenessStaleClaim, model.LivenessBlockedNoPath}
	for i := range want {
		if got.Tasks[i].Liveness != want[i] {
			t.Fatalf("task %s liveness = %s want %s", got.Tasks[i].ID, got.Tasks[i].Liveness, want[i])
		}
	}
	snapshot.Sources[2].State = model.SourceDegraded
	got = policy.ApplyPolicy(snapshot, now)
	if got.Tasks[2].Liveness == model.LivenessStaleClaim || hasRule(got, "task.stale_claim") {
		t.Fatal("degraded GitHub inventory must suppress stale claim")
	}
}

func TestLiveLinkageRequiresCurrentSupportingSource(t *testing.T) {
	snapshot := base()
	snapshot.Sources[2].State = model.SourceStale
	snapshot.Runs = []model.Run{{
		ID: "run", Kind: "workflow", Status: "failed", UpdatedAt: now.Add(-337 * time.Hour),
		DocPath: model.Known("docs/run.md"), Evidence: []model.SafeLink{{URL: "https://example.invalid/pr/1"}},
	}}
	snapshot.Tasks = []model.Task{{
		ID: "task", Slug: "task", Status: "todo", UpdatedAt: now.Add(-337 * time.Hour),
		Artifacts: []model.SafeLink{{URL: "https://example.invalid/pr/1"}},
	}}
	snapshot.PullRequests = []model.PullRequest{{ID: "pr", URL: "https://example.invalid/pr/1", State: "open"}}
	got := policy.ApplyPolicy(snapshot, now)
	if got.Runs[0].Liveness != model.LivenessUnknown {
		t.Fatalf("run liveness = %s want %s", got.Runs[0].Liveness, model.LivenessUnknown)
	}
	if got.Tasks[0].Liveness != model.LivenessUnknown {
		t.Fatalf("task liveness = %s want %s", got.Tasks[0].Liveness, model.LivenessUnknown)
	}
}

func TestStaleClaimExactLinkageAnd336HourBoundary(t *testing.T) {
	for _, test := range []struct {
		name string
		run  *model.Run
		pr   *model.PullRequest
		want model.Liveness
	}{
		{name: "exact run at boundary", run: &model.Run{ID: "run", Task: "task", UpdatedAt: now.Add(-336 * time.Hour)}, want: model.LivenessLive},
		{name: "substring run does not link", run: &model.Run{ID: "run", Task: "task-extra", UpdatedAt: now}, want: model.LivenessStaleClaim},
		{name: "exact open pr", pr: &model.PullRequest{ID: "pr", URL: "https://example.invalid/pr/1", State: "open"}, want: model.LivenessLive},
		{name: "closed pr does not link", pr: &model.PullRequest{ID: "pr", URL: "https://example.invalid/pr/1", State: "closed"}, want: model.LivenessStaleClaim},
	} {
		t.Run(test.name, func(t *testing.T) {
			snapshot := base()
			snapshot.Tasks = []model.Task{{ID: "task", Slug: "task", Status: "in_progress", UpdatedAt: now.Add(-337 * time.Hour), Artifacts: []model.SafeLink{{URL: "https://example.invalid/pr/1"}}}}
			if test.run != nil {
				snapshot.Runs = []model.Run{*test.run}
			}
			if test.pr != nil {
				snapshot.PullRequests = []model.PullRequest{*test.pr}
			}
			got := policy.ApplyPolicy(snapshot, now)
			if got.Tasks[0].Liveness != test.want {
				t.Fatalf("liveness = %s want %s", got.Tasks[0].Liveness, test.want)
			}
		})
	}
}

func TestBlockedTaskPathRequiresExactOpenPR(t *testing.T) {
	t.Run("doc artifact does not suppress no-path", func(t *testing.T) {
		snapshot := base()
		snapshot.Tasks = []model.Task{{
			ID: "blocked", Slug: "blocked", Status: "blocked", UpdatedAt: now,
			Artifacts: []model.SafeLink{{Label: "spec", Path: "docs/features/example/spec.md"}},
		}}
		got := policy.ApplyPolicy(snapshot, now)
		if got.Tasks[0].Liveness != model.LivenessBlockedNoPath {
			t.Fatalf("liveness = %s want %s", got.Tasks[0].Liveness, model.LivenessBlockedNoPath)
		}
	})
	t.Run("exact open pr suppresses no-path", func(t *testing.T) {
		snapshot := base()
		snapshot.Tasks = []model.Task{{
			ID: "blocked", Slug: "blocked", Status: "blocked", UpdatedAt: now,
			Artifacts: []model.SafeLink{{URL: "https://example.invalid/pr/1"}},
		}}
		snapshot.PullRequests = []model.PullRequest{{ID: "pr", URL: "https://example.invalid/pr/1", State: "open"}}
		got := policy.ApplyPolicy(snapshot, now)
		if got.Tasks[0].Liveness == model.LivenessBlockedNoPath {
			t.Fatalf("liveness = %s", got.Tasks[0].Liveness)
		}
	})
}

func TestPRRulesFailClosedAndPreserveNegativeEvidence(t *testing.T) {
	snapshot := base()
	snapshot.PullRequests = []model.PullRequest{
		{ID: "failed", DetailState: "truncated", UpdatedAt: now, Checks: []model.Check{{Status: "COMPLETED", Conclusion: "FAILURE"}}},
		{ID: "empty", DetailState: "complete", UpdatedAt: now, ReviewDecision: "APPROVED", Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN"},
		{ID: "review", DetailState: "complete", UpdatedAt: now, Checks: []model.Check{{Status: "COMPLETED", Conclusion: "SUCCESS"}}, ReviewDecision: "REVIEW_REQUIRED"},
	}
	got := policy.ApplyPolicy(snapshot, now)
	if !hasRule(got, "pr.ci_failed") || !hasRule(got, "pr.detail_truncated") || !hasRule(got, "pr.review_needed") {
		t.Fatalf("rules = %v", ruleIDs(got))
	}
	for _, item := range got.Attention {
		if item.ID == "pr.merge_ready:empty" {
			t.Fatal("empty checks fabricated merge readiness")
		}
	}
}

func TestPullRequestRankingRulesAndScores(t *testing.T) {
	success := []model.Check{{Status: "COMPLETED", Conclusion: "SUCCESS"}}
	running := []model.Check{{Status: "IN_PROGRESS"}}
	tests := []struct {
		name  string
		pr    model.PullRequest
		rule  string
		score int
	}{
		{name: "failed", pr: model.PullRequest{Checks: []model.Check{{Status: "COMPLETED", Conclusion: "FAILURE"}}, ReviewDecision: "CHANGES_REQUESTED", UnresolvedThreads: 1}, rule: "pr.ci_failed", score: 90},
		{name: "changes", pr: model.PullRequest{Checks: success, ReviewDecision: "CHANGES_REQUESTED", UnresolvedThreads: 1}, rule: "pr.changes_requested", score: 85},
		{name: "threads", pr: model.PullRequest{Checks: success, ReviewDecision: "APPROVED", UnresolvedThreads: 1}, rule: "pr.unresolved_threads", score: 75},
		{name: "review", pr: model.PullRequest{DetailState: "complete", Checks: success, ReviewDecision: "REVIEW_REQUIRED"}, rule: "pr.review_needed", score: 70},
		{name: "merge", pr: model.PullRequest{DetailState: "complete", Checks: success, ReviewDecision: "APPROVED", Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN"}, rule: "pr.merge_ready", score: 65},
		{name: "running", pr: model.PullRequest{DetailState: "complete", Checks: running}, rule: "pr.checks_running", score: 30},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := base()
			test.pr.ID, test.pr.UpdatedAt = test.name, now
			snapshot.PullRequests = []model.PullRequest{test.pr}
			got := policy.ApplyPolicy(snapshot, now)
			if len(got.Attention) != 1 || got.Attention[0].RuleID != test.rule || got.Attention[0].Score != test.score {
				t.Fatalf("attention = %+v", got.Attention)
			}
		})
	}
}

func TestPositivePullRequestRulesFailClosed(t *testing.T) {
	for _, pr := range []model.PullRequest{
		{ID: "empty", DetailState: "complete", ReviewDecision: "APPROVED", Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN"},
		{ID: "truncated", DetailState: "truncated", Checks: []model.Check{{Status: "COMPLETED", Conclusion: "SUCCESS"}}, ReviewDecision: "APPROVED", Mergeable: "MERGEABLE", MergeStateStatus: "CLEAN"},
		{ID: "unknown", DetailState: "unknown", Checks: []model.Check{{Status: "IN_PROGRESS"}}},
	} {
		snapshot := base()
		snapshot.PullRequests = []model.PullRequest{pr}
		got := policy.ApplyPolicy(snapshot, now)
		for _, forbidden := range []string{"pr.review_needed", "pr.merge_ready", "pr.checks_running"} {
			if hasRule(got, forbidden) {
				t.Fatalf("%s emitted for %s", forbidden, pr.ID)
			}
		}
	}
}

func TestStaleSourceSuppressesLivenessAndEmitsOnce(t *testing.T) {
	snapshot := base()
	snapshot.Sources[0].State = model.SourceStale
	snapshot.Runs = []model.Run{{ID: "run", Kind: "workflow", Status: "running", UpdatedAt: now.Add(-time.Hour), DocPath: model.Known("docs/a.md")}, {ID: "run2", Kind: "workflow", Status: "running", UpdatedAt: now, DocPath: model.Known("docs/b.md")}}
	got := policy.ApplyPolicy(snapshot, now)
	for _, run := range got.Runs {
		if run.Liveness != model.LivenessUnknown {
			t.Fatalf("stale run liveness = %s", run.Liveness)
		}
	}
	count := 0
	for _, item := range got.Attention {
		if item.RuleID == "source.stale" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("source.stale count = %d", count)
	}
}

func TestReceiptMembershipFailsClosed(t *testing.T) {
	states := []struct {
		name     string
		receipts []model.SourceReceipt
	}{
		{name: "missing", receipts: nil},
		{name: "duplicate", receipts: []model.SourceReceipt{receipt("ship", model.SourceOK), receipt("ship", model.SourceOK)}},
		{name: "loading", receipts: []model.SourceReceipt{receipt("ship", model.SourceLoading)}},
		{name: "unavailable", receipts: []model.SourceReceipt{receipt("ship", model.SourceUnavailable)}},
		{name: "stale", receipts: []model.SourceReceipt{receipt("ship", model.SourceStale)}},
	}
	for _, test := range states {
		t.Run(test.name, func(t *testing.T) {
			snapshot := model.Snapshot{Sources: test.receipts, Runs: []model.Run{{ID: "run", Kind: "workflow", Status: "running", UpdatedAt: now, DocPath: model.Known("docs/a.md")}}}
			got := policy.ApplyPolicy(snapshot, now)
			if got.Runs[0].Liveness != model.LivenessUnknown || hasRule(got, "run.stalled_active") {
				t.Fatalf("dependent result did not fail closed: %+v", got)
			}
		})
	}
}

func TestDegradedReceiptIsCurrentWithoutSourceRule(t *testing.T) {
	snapshot := model.Snapshot{Sources: []model.SourceReceipt{receipt("ship", model.SourceDegraded)}, Runs: []model.Run{{ID: "run", Kind: "workflow", Status: "running", UpdatedAt: now, DocPath: model.Known("docs/a.md")}}}
	got := policy.ApplyPolicy(snapshot, now)
	if got.Runs[0].Liveness != model.LivenessLive || hasRule(got, "source.degraded") {
		t.Fatalf("degraded receipt result = %+v", got)
	}
}

func TestUnavailableSourcesDoNotSuppressHealthySources(t *testing.T) {
	snapshot := base()
	snapshot.Sources = append(snapshot.Sources, receipt("tower", model.SourceUnavailable))
	snapshot.PullRequests = []model.PullRequest{{ID: "failed", UpdatedAt: now, Checks: []model.Check{{Status: "COMPLETED", Conclusion: "FAILURE"}}}}
	got := policy.ApplyPolicy(snapshot, now)
	if !hasRule(got, "source.unavailable") || !hasRule(got, "pr.ci_failed") {
		t.Fatalf("rules = %v", ruleIDs(got))
	}
}

func TestFrictionScoringAndStableTieBreak(t *testing.T) {
	snapshot := base()
	snapshot.ToolHealth = []model.ToolHealth{
		{Tool: "b", Kind: "accumulated_friction", WorstSeverity: "P1", SessionCount: 5, LastOccurrence: now.Add(-72 * time.Hour)},
		{Tool: "a", Kind: "accumulated_friction", WorstSeverity: "P1", SessionCount: 5, LastOccurrence: now.Add(-72 * time.Hour)},
	}
	got := policy.ApplyPolicy(snapshot, now)
	if got.Attention[0].Score != 25 || got.Attention[0].ID != "tool.accumulated_friction:a" {
		t.Fatalf("attention = %+v", got.Attention)
	}
}

func TestFrictionScoreInputsAndBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		severity string
		sessions int
		age      time.Duration
		want     int
	}{
		{name: "p1 recent cap", severity: "P1", sessions: 5, age: 72 * time.Hour, want: 25},
		{name: "p2 idle boundary", severity: "P2", sessions: 1, age: 336 * time.Hour, want: 16},
		{name: "p3 old", severity: "P3", sessions: 2, age: 337 * time.Hour, want: 13},
		{name: "unknown future clamp", severity: "other", sessions: 0, age: -time.Hour, want: 13},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := base()
			snapshot.ToolHealth = []model.ToolHealth{{Tool: test.name, Kind: "accumulated_friction", WorstSeverity: test.severity, SessionCount: test.sessions, LastOccurrence: now.Add(-test.age)}}
			got := policy.ApplyPolicy(snapshot, now)
			if len(got.Attention) != 1 || got.Attention[0].Score != test.want {
				t.Fatalf("attention = %+v", got.Attention)
			}
		})
	}
}

func TestStaleFrictionUsesOccurrenceAndRemainsVisible(t *testing.T) {
	snapshot := base()
	snapshot.Sources[4] = receipt("toolhealth", model.SourceStale)
	snapshot.Sources[4].ObservedAt = now
	snapshot.ToolHealth = []model.ToolHealth{{Tool: "ship", Kind: "accumulated_friction", WorstSeverity: "P2", SessionCount: 1, LastOccurrence: now.Add(-337 * time.Hour)}}
	got := policy.ApplyPolicy(snapshot, now)
	if len(got.Attention) != 2 || got.Attention[0].RuleID != "tool.accumulated_friction" || got.Attention[0].Score != 15 || !got.Attention[0].Stale || got.Attention[1].RuleID != "source.stale" {
		t.Fatalf("attention = %+v", got.Attention)
	}
}

func TestAttentionOrderUsesScoreTimeThenID(t *testing.T) {
	snapshot := base()
	snapshot.ToolHealth = []model.ToolHealth{
		{Tool: "b", Kind: "accumulated_friction", WorstSeverity: "P3", LastOccurrence: now},
		{Tool: "newer", Kind: "accumulated_friction", WorstSeverity: "P3", LastOccurrence: now.Add(time.Minute)},
		{Tool: "a", Kind: "accumulated_friction", WorstSeverity: "P3", LastOccurrence: now},
	}
	got := policy.ApplyPolicy(snapshot, now)
	want := []string{"tool.accumulated_friction:newer", "tool.accumulated_friction:a", "tool.accumulated_friction:b"}
	for i := range want {
		if got.Attention[i].ID != want[i] {
			t.Fatalf("order = %v", ruleIDs(got))
		}
	}
}

func TestTaskReadyRequiresKnownDoneDependencies(t *testing.T) {
	snapshot := base()
	snapshot.Tasks = []model.Task{{ID: "done", Slug: "dep", Status: "done", UpdatedAt: now}, {ID: "ready", Slug: "ready", Status: "todo", UpdatedAt: now, Dependencies: []string{"dep"}}, {ID: "unknown", Slug: "unknown", Status: "todo", UpdatedAt: now, Dependencies: []string{"missing"}}}
	got := policy.ApplyPolicy(snapshot, now)
	if !hasRule(got, "task.ready") {
		t.Fatal("known done dependency did not qualify")
	}
	for _, item := range got.Attention {
		if item.ID == "task.ready:unknown" {
			t.Fatal("missing dependency qualified")
		}
	}
}
