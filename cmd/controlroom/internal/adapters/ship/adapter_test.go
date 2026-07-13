package ship

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/model"
)

type fakeRunner struct {
	outputs map[string][]byte
	calls   [][]string
}

func (f *fakeRunner) Run(_ context.Context, executable string, args ...string) ([]byte, error) {
	call := append([]string{executable}, args...)
	f.calls = append(f.calls, call)
	key := stringsJoin(args)
	output, ok := f.outputs[key]
	if !ok {
		return nil, fmt.Errorf("unexpected call: %s", key)
	}
	return output, nil
}

func TestCollectUsesOnlyInventoryForCompleteRows(t *testing.T) {
	f := &fakeRunner{outputs: map[string][]byte{
		"list --json":        []byte(`{"runs":[{"id":"wf_1","repo":"repo","docPath":"docs/spec.md","status":"succeeded","createdAt":"2026-07-13T10:00:00Z","updatedAt":"2026-07-13T11:00:00Z","worktree":{"branch":"feat"},"phases":[],"observability":{"requested":{"runtime":"local","provider":"cursor","model":{"id":"sonnet"}},"actual":{"runtime":"local","provider":"cursor","model":{"id":"sonnet"}},"startedAt":"2026-07-13T10:01:00Z","durationMs":10,"evidence":{"availability":"available","refs":[{"path":"runs/wf_1/prompt.md"}]}}}]}`),
		"driver list --json": []byte(`{"runs":[{"driverRunId":"drv_1","status":"done","repo":"repo","project":"project","phase":"phase","createdAt":"2026-07-13T09:00:00Z","updatedAt":"2026-07-13T12:00:00Z","manifestRef":"docs/driver.md","batches":[]}]}`),
	}}
	a := New("ship")
	a.runner = f
	got := a.Collect(context.Background())
	if got.Receipt.State != model.SourceOK || len(got.Runs) != 2 {
		t.Fatalf("unexpected result: %#v", got)
	}
	if got.Runs[1].ID != "wf_1" || got.Runs[1].Evidence == nil {
		t.Fatalf("workflow availability was not preserved: %#v", got.Runs[1])
	}
	want := [][]string{{"ship", "list", "--json"}, {"ship", "driver", "list", "--json"}}
	if !reflect.DeepEqual(f.calls, want) {
		t.Fatalf("calls = %#v, want %#v", f.calls, want)
	}
}

func TestCollectCallsStatusOnceForIncompleteValidRow(t *testing.T) {
	complete := `{"id":"wf_1","repo":"repo","docPath":"docs/spec.md","status":"failed","createdAt":"2026-07-13T10:00:00Z","updatedAt":"2026-07-13T11:00:00Z","failureCategory":"test","phases":[],"observability":{"requested":{"runtime":"local","provider":"cursor","model":{"id":"sonnet"}},"actual":{"runtime":"local","provider":"cursor","model":{"id":"sonnet"}},"startedAt":"2026-07-13T10:01:00Z","durationMs":10,"evidence":{"availability":"unavailable"}}}`
	f := &fakeRunner{outputs: map[string][]byte{
		"list --json":        []byte(`{"runs":[{"id":"wf_1","status":"failed"}]}`),
		"status wf_1 --json": []byte(complete),
		"driver list --json": []byte(`{"runs":[]}`),
	}}
	a := New("ship")
	a.runner = f
	got := a.Collect(context.Background())
	if got.Receipt.State != model.SourceOK || len(got.Runs) != 1 || len(f.calls) != 3 {
		t.Fatalf("unexpected result: %#v calls=%#v", got, f.calls)
	}
}

func TestCollectFailsClosedOnMalformedInventory(t *testing.T) {
	a := New("ship")
	a.runner = &fakeRunner{outputs: map[string][]byte{"list --json": []byte(`{"runs":`)}}
	got := a.Collect(context.Background())
	if got.Receipt.State != model.SourceUnavailable || got.Receipt.ErrorCode != "malformed_inventory" || len(got.Runs) != 0 {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestCollectDegradesWhenWorkflowDetailFails(t *testing.T) {
	f := &fakeRunner{outputs: map[string][]byte{
		"list --json":        []byte(`{"runs":[{"id":"wf_1","status":"failed"}]}`),
		"driver list --json": []byte(`{"runs":[]}`),
	}}
	a := New("ship")
	a.runner = f
	got := a.Collect(context.Background())
	if got.Receipt.State != model.SourceDegraded || got.Receipt.ErrorCode != "partial_detail" || len(got.Runs) != 1 {
		t.Fatalf("unexpected result: %#v", got)
	}
}

func TestCollectDriverDetailNormalizesAttemptTimeline(t *testing.T) {
	f := &fakeRunner{outputs: map[string][]byte{
		"list --json":                []byte(`{"runs":[]}`),
		"driver list --json":         []byte(`{"runs":[{"driverRunId":"drv_1","status":"running","repo":"repo","project":"project","phase":"phase","createdAt":"2026-07-13T09:00:00Z","updatedAt":"2026-07-13T12:00:00Z","manifestRef":"docs/driver.md","batches":[{"streams":[{"streamId":"ds_1","status":"done","createdAt":"2026-07-13T09:00:00Z","updatedAt":"2026-07-13T11:00:00Z"}]}]}]}`),
		"driver status drv_1 --json": []byte(`{"driverRunId":"drv_1","status":"running","repo":"repo","project":"project","phase":"phase","createdAt":"2026-07-13T09:00:00Z","updatedAt":"2026-07-13T12:00:00Z","manifestRef":"docs/driver.md","batches":[{"streams":[{"streamId":"ds_1","status":"done","createdAt":"2026-07-13T09:00:00Z","updatedAt":"2026-07-13T11:00:00Z","attempts":[{"dispatchedAt":"2026-07-13T09:30:00Z","terminal":true,"endedAt":"2026-07-13T10:00:00Z"}]}]}]}`),
	}}
	a := New("ship")
	a.runner = f
	got := a.Collect(context.Background())
	if got.Receipt.State != model.SourceOK || len(got.Runs) != 2 {
		t.Fatalf("unexpected result: %#v", got)
	}
	var child model.Run
	for _, run := range got.Runs {
		if run.ID == "ds_1" {
			child = run
		}
	}
	if child.StartedAt.State != model.Available || child.EndedAt.State != model.Available || child.DurationMS.Value == nil || *child.DurationMS.Value != int64(30*time.Minute/time.Millisecond) {
		t.Fatalf("attempt timeline missing: %#v", child)
	}
}

func TestNormalizeWorkflowFallsBackToOwnerEndTime(t *testing.T) {
	ended := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	run := normalizeWorkflow(workflowWire{ID: "wf_1", EndedAt: &ended, Observability: &observability{}})
	if run.EndedAt.State != model.Available || run.EndedAt.Value == nil || !run.EndedAt.Value.Equal(ended) {
		t.Fatalf("end time was discarded: %#v", run.EndedAt)
	}
}

func TestCollectUnconfiguredAndSanitization(t *testing.T) {
	got := New("").Collect(context.Background())
	if got.Receipt.ErrorCode != "not_configured" {
		t.Fatalf("unexpected result: %#v", got)
	}
	for _, failure := range []string{"token=secret", "api_key=secret", "Bearer secret", "Authorization: secret", "https://user:pass@example.com"} {
		if sanitizeFailure(failure) != "failure detail redacted" {
			t.Fatalf("failure was not redacted: %q", failure)
		}
	}
	for _, id := range []string{"wf_ --delete", "wf_a/b", "wf_"} {
		if validID(id, "wf_") {
			t.Fatalf("unsafe ID accepted: %q", id)
		}
	}
}

func stringsJoin(args []string) string {
	return strings.Join(args, " ")
}
