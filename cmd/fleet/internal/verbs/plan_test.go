package verbs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func planFixture(t *testing.T) workPlan {
	t.Helper()
	repo, _ := requestFixture(t)
	var work []desiredWork
	for _, name := range []string{"ivy", "rooms", "roxiq"} {
		runGit(t, repo, "branch", name)
		work = append(work, desiredWork{Name: name, Repo: repo, Change: name, For: "lead", As: "draft", Brief: "Document " + name, Due: "2030-01-01T12:00:00Z"})
	}
	p, err := buildWorkPlan(workIntent{Schema: intentSchema, Work: work})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func planRowPath(a workAction) string { return dispatchFile(a.RepoID, a.Work.Change, a.Work.As) }
func assertStatuses(t *testing.T, results []workResult, want ...string) {
	t.Helper()
	if len(results) != len(want) {
		t.Fatalf("results: %+v", results)
	}
	for i, r := range results {
		if r.Status != want[i] {
			t.Fatalf("results: %+v; want %v", results, want)
		}
	}
}
func TestPlanPartialFailureRetryPreservesCommittedBytes(t *testing.T) {
	p := planFixture(t)
	// Block the atomic writer temporary path, after the precondition read succeeds.
	blocked := fmt.Sprintf("%s.%d.tmp", planRowPath(p.Actions[1]), os.Getpid())
	if err := os.MkdirAll(blocked, 0700); err != nil {
		t.Fatal(err)
	}
	results, err := applyWorkPlan(p, p.Digest)
	if err == nil {
		t.Fatal("expected partial failure")
	}
	assertStatuses(t, results, "recorded", "unknown", "not attempted")
	before := readBytes(t, planRowPath(p.Actions[0]))
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	// Lost response/retry does not consult a global agenda or rewrite original due/head.
	runGit(t, p.Actions[0].Work.Repo, "branch", "-f", "ivy", "task")
	results, err = applyWorkPlan(p, p.Digest)
	if err != nil {
		t.Fatal(err)
	}
	assertStatuses(t, results, "already recorded", "recorded", "recorded")
	if !bytes.Equal(before, readBytes(t, planRowPath(p.Actions[0]))) {
		t.Fatal("retry rewrote committed row")
	}
}
func TestPlanSameOwnerChangedBriefAndDeadlineConflict(t *testing.T) {
	p := planFixture(t)
	if _, err := applyWorkPlan(p, p.Digest); err != nil {
		t.Fatal(err)
	}
	before := readBytes(t, planRowPath(p.Actions[0]))
	w := p.Actions[0].Work
	w.Brief = "Different scope"
	w.Due = "2030-01-02T12:00:00Z"
	changed, err := buildWorkPlan(workIntent{Schema: intentSchema, Work: []desiredWork{w}})
	if err != nil {
		t.Fatal(err)
	}
	if changed.Actions[0].Action != "conflict" || !strings.Contains(changed.Actions[0].Reason, "brief, due") {
		t.Fatalf("%+v", changed.Actions)
	}
	if _, err := applyWorkPlan(changed, changed.Digest); err == nil {
		t.Fatal("conflicting apply allowed")
	}
	if !bytes.Equal(before, readBytes(t, planRowPath(p.Actions[0]))) {
		t.Fatal("changed authoritative row")
	}
}
func TestPlanHeadDriftStopsRemainderAndReplanKeepsFirst(t *testing.T) {
	p := planFixture(t)
	if status, err := applyWork(p, p.Actions[0]); err != nil || status != "recorded" {
		t.Fatal(status, err)
	}
	repo := p.Actions[1].Work.Repo
	runGit(t, repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "intervening change")
	runGit(t, repo, "branch", "-f", "rooms", "task")
	results, err := applyWorkPlan(p, p.Digest)
	if err == nil {
		t.Fatal("stale head accepted")
	}
	assertStatuses(t, results, "already recorded", "conflict", "not attempted")
	var work []desiredWork
	for _, a := range p.Actions {
		work = append(work, a.Work)
	}
	next, err := buildWorkPlan(workIntent{Schema: intentSchema, Work: work})
	if err != nil {
		t.Fatal(err)
	}
	if next.Actions[0].Action != "keep" {
		t.Fatal(next.Actions)
	}
	results, err = applyWorkPlan(next, next.Digest)
	if err != nil {
		t.Fatal(err)
	}
	assertStatuses(t, results, "kept", "recorded", "recorded")
}
func TestPlanConcurrentDifferentIntentsOnlyOneWins(t *testing.T) {
	p := planFixture(t)
	p.Actions = p.Actions[:1]
	p.Digest = planDigest(p)
	other := p
	other.Actions = append([]workAction(nil), p.Actions...)
	other.Actions[0].Work.Brief = "Other writer"
	other.Digest = planDigest(other)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, plan := range []workPlan{p, other} {
		wg.Add(1)
		go func(plan workPlan) {
			defer wg.Done()
			<-start
			_, err := applyWorkPlan(plan, plan.Digest)
			results <- err
		}(plan)
	}
	close(start)
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("%d writers succeeded", successes)
	}
}
func TestPlanRejectsTamperWrongStoreReadOnlyAndChangedKeep(t *testing.T) {
	p := planFixture(t)
	if _, err := applyWorkPlan(p, "wrong"); err == nil {
		t.Fatal("wrong digest accepted")
	}
	p.Actions[0].Work.Brief = "tampered"
	if _, err := applyWorkPlan(p, p.Digest); err == nil {
		t.Fatal("tamper accepted")
	}
	p.Digest = planDigest(p)
	old := fleet.State
	fleet.State = filepath.Join(old, "other")
	if _, err := applyWorkPlan(p, p.Digest); err == nil {
		t.Fatal("wrong store accepted")
	}
	fleet.State = old
	fleet.ReadOnly = true
	_, err := applyWorkPlan(p, p.Digest)
	fleet.ReadOnly = false
	if err == nil {
		t.Fatal("read-only apply accepted")
	}
	if _, err := applyWorkPlan(p, p.Digest); err != nil {
		t.Fatal(err)
	}
	keep, err := buildWorkPlan(workIntent{Schema: intentSchema, Work: []desiredWork{p.Actions[0].Work}})
	if err != nil {
		t.Fatal(err)
	}
	row := fleet.ReadJSON(planRowPath(p.Actions[0]))
	row["brief"] = "outside edit"
	if err := fleet.WriteJSON(planRowPath(p.Actions[0]), row); err != nil {
		t.Fatal(err)
	}
	if _, err := applyWorkPlan(keep, keep.Digest); err == nil {
		t.Fatal("stale keep accepted")
	}
}
func TestPlanPreviewIsReadOnlyAndRejectsSeat(t *testing.T) {
	p := planFixture(t)
	if _, err := os.Stat(dispatchDir()); !os.IsNotExist(err) {
		t.Fatal("preview created dispatch state")
	}
	path := filepath.Join(t.TempDir(), "input.json")
	if err := os.WriteFile(path, []byte(`{"schema":"fleet-work.v0","work":[],"seat":"worker"}`), 0600); err != nil {
		t.Fatal(err)
	}
	var intent workIntent
	if err := decodePlanJSON(path, &intent); err == nil {
		t.Fatal("unsupported field accepted")
	}
	if err := saveWorkPlan(path, p); err == nil {
		t.Fatal("plan overwrote input")
	}
}

func TestPlanCanonicalBranchAliasCannotCreateAnotherRow(t *testing.T) {
	p := planFixture(t)
	if _, err := applyWorkPlan(p, p.Digest); err != nil {
		t.Fatal(err)
	}
	w := p.Actions[0].Work
	w.Change = "IVY"
	alias, err := buildWorkPlan(workIntent{Schema: intentSchema, Work: []desiredWork{w}})
	if err != nil {
		t.Fatal(err)
	}
	if alias.Actions[0].Work.Change != "ivy" || alias.Actions[0].Action != "keep" {
		t.Fatal(alias.Actions)
	}
	w.Name = "duplicate"
	if _, err := buildWorkPlan(workIntent{Schema: intentSchema, Work: []desiredWork{p.Actions[0].Work, w}}); err == nil {
		t.Fatal("alias bypassed duplicate target check")
	}
	// Even a separately authored, digest-consistent plan must not overwrite via alias.
	p.Actions = p.Actions[:1]
	p.Actions[0].Work.Change = "IVY"
	p.Digest = planDigest(p)
	before := readBytes(t, planRowPath(alias.Actions[0]))
	if _, err := applyWorkPlan(p, p.Digest); err == nil {
		t.Fatal("noncanonical apply allowed")
	}
	if !bytes.Equal(before, readBytes(t, planRowPath(alias.Actions[0]))) {
		t.Fatal("alias overwrote row")
	}
}

func TestPlanLockFailureIsNotAttempted(t *testing.T) {
	p := planFixture(t)
	if err := os.MkdirAll(fleet.State, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleet.Path("keylocks"), []byte("blocked"), 0600); err != nil {
		t.Fatal(err)
	}
	results, err := applyWorkPlan(p, p.Digest)
	if err == nil {
		t.Fatal("lock failure ignored")
	}
	assertStatuses(t, results, "not attempted", "not attempted", "not attempted")
	if _, err := os.Stat(dispatchDir()); !os.IsNotExist(err) {
		t.Fatal("wrote without lock")
	}
}
func TestPlanDoesNotManagePlacementOrRequests(t *testing.T) {
	for _, field := range []string{"slot", "reply_to", "request_id"} {
		t.Run(field, func(t *testing.T) {
			p := planFixture(t)
			if _, err := applyWorkPlan(p, p.Digest); err != nil {
				t.Fatal(err)
			}
			a := p.Actions[0]
			row := fleet.ReadJSON(planRowPath(a))
			row[field] = "occupied"
			if err := fleet.WriteJSON(planRowPath(a), row); err != nil {
				t.Fatal(err)
			}
			next, err := buildWorkPlan(workIntent{Schema: intentSchema, Work: []desiredWork{a.Work}})
			if err != nil {
				t.Fatal(err)
			}
			if next.Actions[0].Action != "conflict" || !strings.Contains(next.Actions[0].Reason, field) {
				t.Fatal(next.Actions)
			}
		})
	}
}

func TestPlanKeepsOrdinaryDispatchFractionalDeadline(t *testing.T) {
	p := planFixture(t)
	w := p.Actions[0].Work
	if err := CmdDispatch(w.Change, w.As, w.For, "45m", "", w.Brief, "", "", false); err != nil {
		t.Fatal(err)
	}
	row := fleet.ReadJSON(planRowPath(p.Actions[0]))
	due := fleet.F(row, "due")
	seconds := int64(due)
	w.Due = time.Unix(seconds, int64((due-float64(seconds))*1e9)).UTC().Format(time.RFC3339Nano)
	next, err := buildWorkPlan(workIntent{Schema: intentSchema, Work: []desiredWork{w}})
	if err != nil {
		t.Fatal(err)
	}
	if next.Actions[0].Action != "keep" {
		t.Fatal(next.Actions)
	}
}
func TestPlanDispatchObservationIsNotRepeatedOnReplay(t *testing.T) {
	p := planFixture(t)
	if _, err := applyWorkPlan(p, p.Digest); err != nil {
		t.Fatal(err)
	}
	before := readBytes(t, fleet.Path("actions.jsonl"))
	if len(bytes.Split(bytes.TrimSpace(before), []byte("\n"))) != 3 {
		t.Fatalf("events: %s", before)
	}
	if _, err := applyWorkPlan(p, p.Digest); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, readBytes(t, fleet.Path("actions.jsonl"))) {
		t.Fatal("replay duplicated observation")
	}
}
func TestPlanDisplayEscapesControlCharacters(t *testing.T) {
	p := planFixture(t)
	w := p.Actions[0].Work
	w.For = "lead\r\x1b[2J"
	w.Brief = "first\nFAKE APPROVAL\x1b[2K"
	input := filepath.Join(t.TempDir(), "intent.json")
	data, err := json.Marshal(workIntent{Schema: intentSchema, Work: []desiredWork{w}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(input, data, 0600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	Out = &buf
	if err := dispatchPlan([]string{"plan", input}); err != nil {
		t.Fatal(err)
	}
	output := buf.String()
	if strings.ContainsAny(output, "\r\x1b") || strings.Contains(output, "\nFAKE APPROVAL") {
		t.Fatalf("unsafe rendering: %q", output)
	}
	if !strings.Contains(output, `first\nFAKE APPROVAL\x1b[2K`) {
		t.Fatalf("lost brief: %q", output)
	}
}
