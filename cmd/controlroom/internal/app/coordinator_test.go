package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

var testNow = time.Date(2026, time.July, 13, 14, 30, 0, 0, time.UTC)

func TestBootstrapNamesEveryConfiguredSource(t *testing.T) {
	coordinator := newTestCoordinator(t, Collectors{})
	snapshot := coordinator.Snapshot()
	if snapshot.Version != 0 || snapshot.Mode != "" || len(snapshot.Sources) != 6 {
		t.Fatalf("bootstrap = %+v", snapshot)
	}
	for _, receipt := range snapshot.Sources {
		if receipt.State != model.SourceLoading {
			t.Fatalf("receipt = %+v", receipt)
		}
	}
}

func TestRefreshJoinsIdenticalIdentityAndCancelsDifferentTrigger(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{}, 2)
	ship := func(ctx context.Context) Result {
		calls.Add(1)
		started <- struct{}{}
		<-ctx.Done()
		return unavailableResult("ship", testNow, "cancelled")
	}
	coordinator := newTestCoordinator(t, Collectors{Ship: ship})
	first, err := coordinator.Refresh("manual")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	joined, err := coordinator.Refresh("manual")
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "started" || joined.Status != "joined" || joined.BaselineVersion != first.BaselineVersion {
		t.Fatalf("receipts = %+v %+v", first, joined)
	}
	second, err := coordinator.Refresh("auto")
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if second.Status != "started" || calls.Load() != 2 {
		t.Fatalf("second = %+v, calls = %d", second, calls.Load())
	}
	coordinator.Close()
}

func TestSupersededLateResultCannotPublish(t *testing.T) {
	var calls atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	coordinator := newTestCoordinator(t, Collectors{Ship: func(context.Context) Result {
		if calls.Add(1) == 1 {
			close(firstStarted)
			<-releaseFirst
			return Result{Receipt: okReceipt("ship", testNow), Runs: []model.Run{{ID: "superseded", Status: "running", UpdatedAt: testNow}}}
		}
		return Result{Receipt: okReceipt("ship", testNow), Runs: []model.Run{{ID: "current", Status: "running", UpdatedAt: testNow}}}
	}})
	_, firstFlight, err := coordinator.start("manual")
	if err != nil {
		t.Fatal(err)
	}
	<-firstStarted
	snapshot, err := coordinator.Collect(t.Context(), "auto")
	if err != nil {
		t.Fatal(err)
	}
	close(releaseFirst)
	<-firstFlight.done
	latest := coordinator.Snapshot()
	if len(snapshot.Runs) != 1 || snapshot.Runs[0].ID != "current" || len(latest.Runs) != 1 || latest.Runs[0].ID != "current" || latest.Version != snapshot.Version {
		t.Fatalf("superseded epoch published: collected=%+v latest=%+v", snapshot.Runs, latest.Runs)
	}
}

func TestManualFlagReachesOnlyManualDossierCollection(t *testing.T) {
	manuals := make(chan bool, 2)
	coordinator := newTestCoordinator(t, Collectors{Dossier: func(_ context.Context, manual bool) Result {
		manuals <- manual
		return Result{Receipt: okReceipt("dossier", testNow)}
	}})
	if _, err := coordinator.Collect(t.Context(), "manual"); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Collect(t.Context(), "auto"); err != nil {
		t.Fatal(err)
	}
	if first, second := <-manuals, <-manuals; !first || second {
		t.Fatalf("manual dispatches = %v, %v", first, second)
	}
}

func TestFailedRefreshRetainsLastCurrentPayloadWithDualReceipts(t *testing.T) {
	failed := false
	run := model.Run{ID: "drv_one", Kind: "driver", Status: "running", UpdatedAt: testNow.Add(-time.Minute)}
	collectors := Collectors{
		Ship: func(context.Context) Result {
			if failed {
				return unavailableResult("ship", testNow.Add(time.Minute), "command_failed")
			}
			return Result{Receipt: okReceipt("ship", testNow), Runs: []model.Run{run}}
		},
	}
	coordinator := newTestCoordinator(t, collectors)
	if _, err := coordinator.Collect(t.Context(), "manual"); err != nil {
		t.Fatal(err)
	}
	failed = true
	snapshot, err := coordinator.Collect(t.Context(), "manual")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Runs) != 1 || snapshot.Runs[0].ID != run.ID || snapshot.Runs[0].Liveness != model.LivenessUnknown {
		t.Fatalf("retained runs = %+v", snapshot.Runs)
	}
	states := sourceStates(snapshot.Sources, "ship")
	if len(states) != 2 || states[0] != model.SourceUnavailable || states[1] != model.SourceStale {
		t.Fatalf("ship states = %v", states)
	}
	if !hasRule(snapshot.Attention, "source.unavailable") || !hasRule(snapshot.Attention, "source.stale") {
		t.Fatalf("attention = %+v", snapshot.Attention)
	}
}

func TestEnrichersRendezvousBeforeOneFinalPublish(t *testing.T) {
	traceRelease := make(chan struct{})
	healthRelease := make(chan struct{})
	coordinator := newTestCoordinator(t, Collectors{
		Tracelens: func(context.Context, []model.Run, model.SourceReceipt) Result {
			<-traceRelease
			return Result{Receipt: okReceipt("tracelens", testNow)}
		},
		ToolHealth: func(context.Context) Result {
			<-healthRelease
			return Result{Receipt: okReceipt("toolhealth", testNow)}
		},
	})
	receipt, err := coordinator.Refresh("manual")
	if err != nil {
		t.Fatal(err)
	}
	waitForVersion(t, coordinator, receipt.BaselineVersion+1)
	close(traceRelease)
	// This asserts a non-event: one enricher finishing has no deterministic
	// publication barrier because publication must wait for both enrichers.
	time.Sleep(10 * time.Millisecond)
	if got := coordinator.Snapshot().Version; got != receipt.BaselineVersion+1 {
		t.Fatalf("partial enrichment published version %d", got)
	}
	close(healthRelease)
	waitForVersion(t, coordinator, receipt.BaselineVersion+2)
	if got := coordinator.Snapshot().Version; got != receipt.BaselineVersion+2 {
		t.Fatalf("final version = %d", got)
	}
}

func TestPublishedSnapshotsOwnNestedData(t *testing.T) {
	evidence := []model.SafeLink{{Label: "report", Path: "reports/one.json"}}
	coordinator := newTestCoordinator(t, Collectors{
		Ship: func(context.Context) Result {
			return Result{Receipt: okReceipt("ship", testNow), Runs: []model.Run{{ID: "wf_one", Kind: "workflow", Status: "done", UpdatedAt: testNow, Evidence: evidence}}}
		},
	})
	first, err := coordinator.Collect(t.Context(), "manual")
	if err != nil {
		t.Fatal(err)
	}
	evidence[0].Label = "mutated input"
	first.Runs[0].Evidence[0].Label = "mutated output"
	second := coordinator.Snapshot()
	if got := second.Runs[0].Evidence[0].Label; got != "report" {
		t.Fatalf("published nested data mutated: %q", got)
	}
}

func TestClosedCoordinatorRejectsRefresh(t *testing.T) {
	coordinator := newTestCoordinator(t, Collectors{})
	coordinator.Close()
	if _, err := coordinator.Refresh("manual"); err == nil {
		t.Fatal("closed coordinator accepted refresh")
	}
}

func newTestCoordinator(t *testing.T, overrides Collectors) *Coordinator {
	t.Helper()
	empty := func(source string) func(context.Context) Result {
		return func(context.Context) Result { return Result{Receipt: okReceipt(source, testNow)} }
	}
	collectors := Collectors{
		Ship:    empty("ship"),
		Dossier: func(context.Context, bool) Result { return Result{Receipt: okReceipt("dossier", testNow)} },
		GitHub:  empty("github"),
		Tower:   empty("tower"),
		Tracelens: func(context.Context, []model.Run, model.SourceReceipt) Result {
			return Result{Receipt: okReceipt("tracelens", testNow)}
		},
		ToolHealth: empty("toolhealth"),
	}
	if overrides.Ship != nil {
		collectors.Ship = overrides.Ship
	}
	if overrides.Dossier != nil {
		collectors.Dossier = overrides.Dossier
	}
	if overrides.GitHub != nil {
		collectors.GitHub = overrides.GitHub
	}
	if overrides.Tower != nil {
		collectors.Tower = overrides.Tower
	}
	if overrides.Tracelens != nil {
		collectors.Tracelens = overrides.Tracelens
	}
	if overrides.ToolHealth != nil {
		collectors.ToolHealth = overrides.ToolHealth
	}
	coordinator, err := New(Config{
		Mode: "real", Fingerprint: "test", Now: func() time.Time { return testNow },
		CoreTimeout: time.Second, EnrichmentTimeout: time.Second, Collectors: collectors,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(coordinator.Close)
	return coordinator
}

func okReceipt(source string, observed time.Time) model.SourceReceipt {
	return model.SourceReceipt{Source: source, State: model.SourceOK, ObservedAt: observed}
}

func unavailableResult(source string, observed time.Time, code string) Result {
	return Result{Receipt: model.SourceReceipt{Source: source, State: model.SourceUnavailable, ObservedAt: observed, ErrorCode: code, Message: "Source unavailable"}}
}

func sourceStates(receipts []model.SourceReceipt, source string) []model.SourceState {
	states := make([]model.SourceState, 0, 2)
	for _, receipt := range receipts {
		if receipt.Source == source {
			states = append(states, receipt.State)
		}
	}
	return states
}

func hasRule(items []model.AttentionItem, rule string) bool {
	for _, item := range items {
		if item.RuleID == rule {
			return true
		}
	}
	return false
}

func waitForVersion(t *testing.T, coordinator *Coordinator, version uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if coordinator.Snapshot().Version >= version {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("version %d did not publish; got %d", version, coordinator.Snapshot().Version)
}
