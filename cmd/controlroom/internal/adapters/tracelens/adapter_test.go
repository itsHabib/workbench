package tracelens

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

type fakeRunner struct{ ids []string }

func (f *fakeRunner) Run(_ context.Context, _ string, args ...string) ([]byte, error) {
	id := args[len(args)-1]
	f.ids = append(f.ids, id)
	return []byte(fmt.Sprintf(`{"run_id":%q,"verdict":"pass","tier":"T0","dialect":"ship-trace-v1","findings":[],"report":{"state":"unknown"},"evidence":[],"input_tokens":{"state":"unknown"},"output_tokens":{"state":"unknown"},"cost_usd":{"state":"unavailable"},"latency_ms":{"state":"unavailable"}}`, id)), nil
}

func TestCollectAppliesEligibilityOrderingAndCap(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	runs := make([]model.Run, 0, 9)
	for i := 0; i < 7; i++ {
		runs = append(runs, model.Run{ID: fmt.Sprintf("wf_%d", i), Kind: "workflow", Status: "succeeded", UpdatedAt: now.Add(-time.Duration(i) * time.Hour), Evidence: []model.SafeLink{}})
	}
	runs = append(runs,
		model.Run{ID: "driver", Kind: "driver", Status: "failed", UpdatedAt: now, Evidence: []model.SafeLink{}},
		model.Run{ID: "no-evidence", Kind: "workflow", Status: "failed", UpdatedAt: now, Evidence: nil},
	)
	f := &fakeRunner{}
	a := New("tracelens")
	a.runner, a.now = f, func() time.Time { return now }
	got := a.Collect(context.Background(), runs, model.SourceReceipt{Source: "ship", State: model.SourceOK})
	if got.Receipt.State != model.SourceOK || len(got.Diagnoses) != 5 {
		t.Fatalf("unexpected result: %#v", got)
	}
	want := []string{"wf_0", "wf_1", "wf_2", "wf_3", "wf_4"}
	for i := range want {
		if f.ids[i] != want[i] {
			t.Fatalf("call order = %#v, want %#v", f.ids, want)
		}
	}
}

func TestCollectDoesNotRunWhenShipIsIncomplete(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	f := &fakeRunner{}
	a := New("tracelens")
	a.runner = f
	a.now = func() time.Time { return now }
	got := a.Collect(context.Background(), []model.Run{{ID: "wf", Kind: "workflow", Status: "failed", UpdatedAt: now, Evidence: []model.SafeLink{}}}, model.SourceReceipt{Source: "ship", State: model.SourceDegraded})
	if got.Receipt.ErrorCode != "ship_not_current" || len(f.ids) != 0 {
		t.Fatalf("unexpected result: %#v, calls=%#v", got, f.ids)
	}
}

func TestCollectRedactsSensitiveLinkLabels(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	a := New("tracelens")
	a.now = func() time.Time { return now }
	a.runner = runnerFunc(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(`{"run_id":"wf","verdict":"pass","tier":"T0","dialect":"ship-trace-v1","findings":[],"report":{"state":"available","value":{"label":"Bearer secret","url":"https://example.invalid/report"}},"evidence":[{"label":"token=secret","url":"https://example.invalid/evidence"}],"input_tokens":{"state":"unknown"},"output_tokens":{"state":"unknown"},"cost_usd":{"state":"unknown"},"latency_ms":{"state":"unknown"}}`), nil
	})
	got := a.Collect(context.Background(), []model.Run{{ID: "wf", Kind: "workflow", Status: "succeeded", UpdatedAt: now, Evidence: []model.SafeLink{}}}, model.SourceReceipt{Source: "ship", State: model.SourceOK})
	if got.Diagnoses[0].Evidence[0].Label != "[redacted]" || got.Diagnoses[0].Report.Value.Label != "[redacted]" {
		t.Fatalf("sensitive labels survived: %#v", got.Diagnoses[0])
	}
}

func TestCollectRedactsSensitiveFindingText(t *testing.T) {
	now := time.Now()
	a := New("tracelens")
	a.now = func() time.Time { return now }
	a.runner = runnerFunc(func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return []byte(`{"run_id":"wf","verdict":"fail","tier":"T1","dialect":"ship-trace-v1","findings":[{"title":"leak","evidence":"C:\\Users\\operator\\trace.json"}],"report":{"state":"unknown"},"evidence":[],"input_tokens":{"state":"unknown"},"output_tokens":{"state":"unknown"},"cost_usd":{"state":"unknown"},"latency_ms":{"state":"unknown"}}`), nil
	})
	got := a.Collect(context.Background(), []model.Run{{ID: "wf", Kind: "workflow", Status: "failed", UpdatedAt: now, Evidence: []model.SafeLink{}}}, model.SourceReceipt{Source: "ship", State: model.SourceOK})
	if got.Diagnoses[0].Findings[0].Evidence != "[redacted]" {
		t.Fatalf("finding was not redacted: %#v", got.Diagnoses[0])
	}
}

type runnerFunc func(context.Context, string, ...string) ([]byte, error)

func (f runnerFunc) Run(ctx context.Context, executable string, args ...string) ([]byte, error) {
	return f(ctx, executable, args...)
}
