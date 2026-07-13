// Package policy derives deterministic liveness and attention from source facts.
package policy

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

const (
	shipSource       = "ship"
	dossierSource    = "dossier"
	githubSource     = "github"
	tracelensSource  = "tracelens"
	toolhealthSource = "toolhealth"
)

type receipts struct {
	bySource map[string]model.SourceReceipt
	counts   map[string]int
	states   map[string]map[model.SourceState]int
}

// ApplyPolicy returns a copy of snapshot with deterministic liveness and attention.
func ApplyPolicy(snapshot model.Snapshot, now time.Time) model.Snapshot {
	out := snapshot
	out.Sources = slices.Clone(snapshot.Sources)
	out.Runs = slices.Clone(snapshot.Runs)
	out.Tasks = slices.Clone(snapshot.Tasks)
	out.PullRequests = slices.Clone(snapshot.PullRequests)
	out.Reliability = slices.Clone(snapshot.Reliability)
	out.ToolHealth = slices.Clone(snapshot.ToolHealth)
	out.Repositories = slices.Clone(snapshot.Repositories)
	r := indexReceipts(out.Sources)
	applyRunLiveness(out.Runs, out.PullRequests, r, now)
	applyRunOperatorState(out.Runs, r, now)
	applyTaskLiveness(out.Tasks, out.Runs, out.PullRequests, r, now)
	out.Attention = rank(out, r, now)
	return out
}

func indexReceipts(values []model.SourceReceipt) receipts {
	r := receipts{bySource: make(map[string]model.SourceReceipt), counts: make(map[string]int), states: make(map[string]map[model.SourceState]int)}
	for _, value := range values {
		r.counts[value.Source]++
		r.bySource[value.Source] = value
		if r.states[value.Source] == nil {
			r.states[value.Source] = make(map[model.SourceState]int)
		}
		r.states[value.Source][value.State]++
	}
	return r
}

func (r receipts) retained(source string) bool {
	states := r.states[source]
	if states[model.SourceStale] != 1 {
		return false
	}
	return r.counts[source] == 1 || r.counts[source] == 2 &&
		(states[model.SourceLoading] == 1 || states[model.SourceUnavailable] == 1)
}

func (r receipts) current(source string) bool {
	receipt, ok := r.bySource[source]
	return ok && r.counts[source] == 1 && (receipt.State == model.SourceOK || receipt.State == model.SourceDegraded)
}

func elapsed(now, then time.Time) time.Duration {
	d := now.Sub(then)
	if d < 0 {
		return 0
	}
	return d
}

func elapsedKnown(now, then time.Time) (time.Duration, bool) {
	if then.IsZero() {
		return 0, false
	}
	return elapsed(now, then), true
}

func activeRun(status string) bool {
	switch strings.ToLower(status) {
	case "pending", "running", "dispatching", "dispatched":
		return true
	default:
		return false
	}
}

func waitRun(run model.Run) bool {
	return waitToken(run.Status) || waitToken(run.Phase)
}

func waitToken(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "waiting", "awaiting_review", "awaiting_judgment", "blocked_on_merges", "merge_ready_awaiting_authority", "review", "approval", "judgment":
		return true
	default:
		return false
	}
}

func terminalRun(status string) (model.OperatorState, bool) {
	switch strings.ToLower(status) {
	case "failed", "error", "cancelled", "canceled":
		return model.OperatorFailed, true
	case "done", "completed", "succeeded", "success":
		return model.OperatorDone, true
	default:
		return "", false
	}
}

func runIdentity(run model.Run) string {
	switch run.Kind {
	case "workflow":
		if run.DocPath.State == model.Available && run.DocPath.Value != nil && *run.DocPath.Value != "" {
			return "workflow:" + *run.DocPath.Value
		}
	case "driver":
		if run.SpecPath.State == model.Available && run.SpecPath.Value != nil && *run.SpecPath.Value != "" {
			return "driver:" + *run.SpecPath.Value
		}
	}
	return ""
}

func retryGroups(runs []model.Run, now time.Time) map[string]int {
	groups := make(map[string]int)
	for _, run := range runs {
		age, ok := elapsedKnown(now, run.UpdatedAt)
		if !ok {
			continue
		}
		identity := runIdentity(run)
		if identity != "" && strings.EqualFold(run.Status, "failed") && age <= 72*time.Hour {
			groups[identity]++
		}
	}
	return groups
}

func applyRunLiveness(runs []model.Run, prs []model.PullRequest, r receipts, now time.Time) {
	groups := retryGroups(runs, now)
	for i := range runs {
		run := &runs[i]
		run.Liveness = model.LivenessUnknown
		if !r.current(shipSource) {
			continue
		}
		age, ok := elapsedKnown(now, run.UpdatedAt)
		if identity := runIdentity(*run); ok && identity != "" && groups[identity] >= 3 && strings.EqualFold(run.Status, "failed") && age <= 72*time.Hour {
			run.Liveness = model.LivenessRetryLoop
			continue
		}
		if waitRun(*run) {
			run.Liveness = model.LivenessLive
			continue
		}
		if ok && activeRun(run.Status) && age >= 15*time.Minute {
			run.Liveness = model.LivenessStalledActive
			continue
		}
		if activeRun(run.Status) || ok && age <= 72*time.Hour || r.current(githubSource) && runHasOpenPR(*run, prs) {
			run.Liveness = model.LivenessLive
			continue
		}
		if ok && age <= 336*time.Hour {
			run.Liveness = model.LivenessIdle
		}
	}
}

func applyRunOperatorState(runs []model.Run, r receipts, now time.Time) {
	for i := range runs {
		run := &runs[i]
		run.OperatorState = model.OperatorUnknown
		run.NextAction = "Revalidate Ship source truth before acting"
		if !r.current(shipSource) {
			continue
		}
		if state, terminal := terminalRun(run.Status); terminal {
			run.OperatorState = state
			if state == model.OperatorDone {
				run.NextAction = "No action required"
			} else {
				run.NextAction = "Inspect failure evidence and ownership before deciding whether retry is safe"
			}
			continue
		}
		if waitRun(*run) {
			run.OperatorState = model.OperatorWaiting
			run.NextAction = "Inspect the named wait boundary and its owner"
			continue
		}
		age, known := elapsedKnown(now, run.UpdatedAt)
		if known && activeRun(run.Status) && age >= 15*time.Minute {
			run.OperatorState = model.OperatorStalled
			run.NextAction = "Inspect current phase evidence and owner before intervention"
			continue
		}
		if known && activeRun(run.Status) && age < 15*time.Minute {
			run.OperatorState = model.OperatorProgressing
			run.NextAction = "Monitor for the next durable update"
		}
	}
}

func applyTaskLiveness(tasks []model.Task, runs []model.Run, prs []model.PullRequest, r receipts, now time.Time) {
	knownTasks := make(map[string]bool)
	for _, task := range tasks {
		knownTasks[task.ID] = true
		knownTasks[task.Slug] = true
	}
	for i := range tasks {
		task := &tasks[i]
		task.Liveness = model.LivenessUnknown
		if !r.current(dossierSource) {
			continue
		}
		if task.Status == "done" {
			task.Liveness = model.LivenessDone
			continue
		}
		if task.Status == "blocked" && !taskHasPath(*task, knownTasks) {
			task.Liveness = model.LivenessBlockedNoPath
			continue
		}
		age, ok := elapsedKnown(now, task.UpdatedAt)
		if ok && (task.Status == "claimed" || task.Status == "in_progress") && age > 336*time.Hour &&
			staleClaimSourcesOK(r) && !taskHasRecentWork(*task, runs, prs, now) {
			task.Liveness = model.LivenessStaleClaim
			continue
		}
		if taskIsLive(*task, runs, prs, r, now, age, ok) {
			task.Liveness = model.LivenessLive
			continue
		}
		if ok && age <= 336*time.Hour {
			task.Liveness = model.LivenessIdle
		}
	}
}

func taskIsLive(task model.Task, runs []model.Run, prs []model.PullRequest, r receipts, now time.Time, age time.Duration, ageKnown bool) bool {
	recentTask := ageKnown && age <= 72*time.Hour
	linkedPR := r.current(githubSource) && taskHasOpenPR(task, prs)
	linkedRun := r.current(shipSource) && taskHasCurrentRun(task, runs, now)
	return recentTask || linkedPR || linkedRun
}

func staleClaimSourcesOK(r receipts) bool {
	for _, source := range []string{dossierSource, shipSource, githubSource} {
		if !r.current(source) || r.bySource[source].State != model.SourceOK {
			return false
		}
	}
	return true
}

func taskHasPath(task model.Task, known map[string]bool) bool {
	for _, dependency := range task.Dependencies {
		if known[dependency] {
			return true
		}
	}
	for _, artifact := range task.Artifacts {
		if artifact.URL != "" || artifact.Path != "" {
			return true
		}
	}
	return false
}

func taskMatchesRun(task model.Task, run model.Run) bool {
	return run.Task == task.ID || run.Task == task.Slug || run.Spec == task.ID || run.Spec == task.Slug
}

func taskHasCurrentRun(task model.Task, runs []model.Run, now time.Time) bool {
	for _, run := range runs {
		age, ok := elapsedKnown(now, run.UpdatedAt)
		if taskMatchesRun(task, run) && ok && age <= 336*time.Hour {
			return true
		}
	}
	return false
}

func linksContain(links []model.SafeLink, value string) bool {
	for _, link := range links {
		if link.URL == value || link.Path == value {
			return true
		}
	}
	return false
}

func taskHasOpenPR(task model.Task, prs []model.PullRequest) bool {
	for _, pr := range prs {
		if strings.EqualFold(pr.State, "open") && linksContain(task.Artifacts, pr.URL) {
			return true
		}
	}
	return false
}

func runHasOpenPR(run model.Run, prs []model.PullRequest) bool {
	for _, pr := range prs {
		if strings.EqualFold(pr.State, "open") && linksContain(run.Evidence, pr.URL) {
			return true
		}
	}
	return false
}

func taskHasRecentWork(task model.Task, runs []model.Run, prs []model.PullRequest, now time.Time) bool {
	return taskHasOpenPR(task, prs) || taskHasCurrentRun(task, runs, now)
}

func rank(snapshot model.Snapshot, r receipts, now time.Time) []model.AttentionItem {
	items := runAttention(snapshot.Runs, r, now)
	items = append(items, pullRequestAttention(snapshot.PullRequests, r)...)
	items = append(items, taskAttention(snapshot.Tasks, r)...)
	items = append(items, toolAttention(snapshot.ToolHealth, r, now)...)
	items = append(items, sourceAttention(snapshot.Sources, r)...)
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Score != items[j].Score {
			return items[i].Score > items[j].Score
		}
		if !items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].UpdatedAt.After(items[j].UpdatedAt)
		}
		return items[i].ID < items[j].ID
	})
	return items
}

func runAttention(runs []model.Run, r receipts, now time.Time) []model.AttentionItem {
	if !r.current(shipSource) {
		return nil
	}
	items := make([]model.AttentionItem, 0)
	groups := retryGroups(runs, now)
	for _, run := range runs {
		age, ok := elapsedKnown(now, run.UpdatedAt)
		identity := runIdentity(run)
		switch {
		case ok && identity != "" && groups[identity] >= 3 && strings.EqualFold(run.Status, "failed") && age <= 72*time.Hour:
			items = append(items, item("run.retry_loop", run.ID, "urgent", 100, "Repeated run failures", fmt.Sprintf("%d failures for %s within 72h", groups[identity], identity), run.Repository, run.Project, run.UpdatedAt, []string{shipSource}))
		case waitRun(run):
			continue
		case ok && activeRun(run.Status) && age >= 15*time.Minute:
			items = append(items, item("run.stalled_active", run.ID, "urgent", 95, "Active run has stalled", "Control Room policy: no source update for 15m", run.Repository, run.Project, run.UpdatedAt, []string{shipSource}))
		}
	}
	return items
}

func pullRequestAttention(prs []model.PullRequest, r receipts) []model.AttentionItem {
	if !r.current(githubSource) {
		return nil
	}
	items := make([]model.AttentionItem, 0)
	for _, pr := range prs {
		if primary, ok := primaryPullRequestAttention(pr); ok {
			items = append(items, primary)
		}
		if pr.DetailState == "truncated" {
			reason := "Pull request detail is truncated"
			if len(pr.TruncatedConnections) > 0 {
				reason += ": " + strings.Join(pr.TruncatedConnections, ", ")
			}
			items = append(items, item("pr.detail_truncated", pr.ID, "informational", 6, "Pull request detail truncated", reason, pr.Repository, "", pr.UpdatedAt, []string{githubSource}))
		}
	}
	return items
}

func primaryPullRequestAttention(pr model.PullRequest) (model.AttentionItem, bool) {
	failed, running, allSuccess := checkFacts(pr.Checks)
	switch {
	case failed:
		return item("pr.ci_failed", pr.ID, "urgent", 90, "Pull request CI failed", "At least one visible check completed with failure", pr.Repository, "", pr.UpdatedAt, []string{githubSource}), true
	case pr.ReviewDecision == "CHANGES_REQUESTED":
		return item("pr.changes_requested", pr.ID, "urgent", 85, "Changes requested", "Current GitHub review decision requests changes", pr.Repository, "", pr.UpdatedAt, []string{githubSource}), true
	case pr.UnresolvedThreads > 0:
		return item("pr.unresolved_threads", pr.ID, "actionable", 75, "Unresolved review threads", fmt.Sprintf("%d unresolved threads", pr.UnresolvedThreads), pr.Repository, "", pr.UpdatedAt, []string{githubSource}), true
	case pullRequestNeedsReview(pr, allSuccess):
		return item("pr.review_needed", pr.ID, "actionable", 70, "Review needed", "Checks passed and review is still required", pr.Repository, "", pr.UpdatedAt, []string{githubSource}), true
	case pullRequestMergeReady(pr, allSuccess):
		return item("pr.merge_ready", pr.ID, "actionable", 65, "Pull request is merge ready", "Checks, review, mergeability, and thread gates are satisfied", pr.Repository, "", pr.UpdatedAt, []string{githubSource}), true
	case pullRequestChecksRunning(pr, running):
		return item("pr.checks_running", pr.ID, "waiting", 30, "Checks are running", "Visible checks are still in progress", pr.Repository, "", pr.UpdatedAt, []string{githubSource}), true
	default:
		return model.AttentionItem{}, false
	}
}

func pullRequestNeedsReview(pr model.PullRequest, allSuccess bool) bool {
	needsReviewer := pr.ReviewDecision == "REVIEW_REQUIRED" || pr.RequestedReviewers > 0
	return pr.DetailState == "complete" && !pr.Draft && allSuccess && needsReviewer
}

func pullRequestMergeReady(pr model.PullRequest, allSuccess bool) bool {
	approved := pr.ReviewDecision == "APPROVED" && pr.RequestedReviewers == 0
	mergeable := pr.Mergeable == "MERGEABLE" && pr.MergeStateStatus == "CLEAN"
	return pr.DetailState == "complete" && !pr.Draft && allSuccess && approved && mergeable && pr.UnresolvedThreads == 0
}

func pullRequestChecksRunning(pr model.PullRequest, running bool) bool {
	return pr.DetailState == "complete" && running && pr.ReviewDecision != "CHANGES_REQUESTED" && pr.UnresolvedThreads == 0
}

func taskAttention(tasks []model.Task, r receipts) []model.AttentionItem {
	if !r.current(dossierSource) {
		return nil
	}
	items := make([]model.AttentionItem, 0)
	done := make(map[string]bool)
	for _, task := range tasks {
		if task.Status == "done" {
			done[task.ID], done[task.Slug] = true, true
		}
	}
	for _, task := range tasks {
		switch task.Liveness {
		case model.LivenessBlockedNoPath:
			items = append(items, item("task.blocked_no_path", task.ID, "urgent", 80, "Blocked task has no path", "No resolvable dependency or artifact explains a path forward", "", task.Project, task.UpdatedAt, []string{dossierSource}))
		case model.LivenessStaleClaim:
			items = append(items, item("task.stale_claim", task.ID, "actionable", 55, "Task claim is stale", "Claim is older than 336h with no exact linked current work", "", task.Project, task.UpdatedAt, []string{dossierSource, githubSource, shipSource}))
		default:
			if task.Status == "todo" && dependenciesDone(task.Dependencies, done) {
				items = append(items, item("task.ready", task.ID, "actionable", 40, "Task is ready", "Every declared dependency is known and done", "", task.Project, task.UpdatedAt, []string{dossierSource}))
			}
		}
	}
	return items
}

func toolAttention(healthRows []model.ToolHealth, r receipts, now time.Time) []model.AttentionItem {
	toolStale := r.retained(toolhealthSource)
	if !r.current(toolhealthSource) && !toolStale {
		return nil
	}
	items := make([]model.AttentionItem, 0)
	for _, health := range healthRows {
		if health.Kind != "accumulated_friction" {
			continue
		}
		score := frictionScore(health, now)
		entry := item("tool.accumulated_friction", health.Tool, "informational", score, "Accumulated tool friction", strings.Join(health.Pain, "; "), "", "", health.LastOccurrence, []string{toolhealthSource})
		entry.Stale = toolStale || health.Stale
		items = append(items, entry)
	}
	return items
}

func sourceAttention(sources []model.SourceReceipt, r receipts) []model.AttentionItem {
	items := make([]model.AttentionItem, 0)
	for _, receipt := range sources {
		if r.states[receipt.Source][receipt.State] != 1 {
			continue
		}
		switch receipt.State {
		case model.SourceUnavailable:
			items = append(items, item("source.unavailable", receipt.Source, "informational", 8, receipt.Source+" unavailable", receipt.Message, "", "", receipt.ObservedAt, []string{receipt.Source}))
		case model.SourceStale:
			entry := item("source.stale", receipt.Source, "informational", 7, receipt.Source+" data is stale", "Retained data requires revalidation", "", "", receipt.ObservedAt, []string{receipt.Source})
			entry.Stale = true
			items = append(items, entry)
		}
	}
	return items
}

func item(rule, entity, category string, score int, title, reason, repo, project string, updated time.Time, sources []string) model.AttentionItem {
	return model.AttentionItem{ID: rule + ":" + entity, RuleID: rule, Category: category, Score: score, Title: title, Reason: reason, Repository: repo, Project: project, UpdatedAt: updated, SupportingSources: sources, Links: []model.SafeLink{}}
}

func checkFacts(checks []model.Check) (failed, running, allSuccess bool) {
	if len(checks) == 0 {
		return false, false, false
	}
	allSuccess = true
	for _, check := range checks {
		status, conclusion := strings.ToUpper(check.Status), strings.ToUpper(check.Conclusion)
		if status == "QUEUED" || status == "PENDING" || status == "IN_PROGRESS" {
			running, allSuccess = true, false
		}
		if status != "COMPLETED" || conclusion != "SUCCESS" {
			allSuccess = false
		}
		if status == "COMPLETED" && (conclusion == "FAILURE" || conclusion == "CANCELLED" || conclusion == "TIMED_OUT" || conclusion == "ACTION_REQUIRED") {
			failed = true
		}
	}
	return failed, running, allSuccess
}

func dependenciesDone(dependencies []string, done map[string]bool) bool {
	for _, dependency := range dependencies {
		if !done[dependency] {
			return false
		}
	}
	return true
}

func frictionScore(health model.ToolHealth, now time.Time) int {
	severity := map[string]int{"P1": 8, "P2": 5, "P3": 2}[strings.ToUpper(health.WorstSeverity)]
	recurrence := health.SessionCount - 1
	if recurrence < 0 {
		recurrence = 0
	}
	if recurrence > 4 {
		recurrence = 4
	}
	recency := 0
	age := elapsed(now, health.LastOccurrence)
	if age <= 72*time.Hour {
		recency = 3
	} else if age <= 336*time.Hour {
		recency = 1
	}
	score := 10 + severity + recurrence + recency
	if score > 25 {
		return 25
	}
	return score
}
