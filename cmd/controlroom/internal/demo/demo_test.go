package demo_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/cmd/controlroom/internal/demo"
)

var update = flag.Bool("update", false, "update golden files")

func TestSnapshotGolden(t *testing.T) {
	snapshot := demo.Snapshot()
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	path := filepath.Join("testdata", "snapshot.golden.json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, want) {
		t.Fatal("demo snapshot differs from golden; run go test ./cmd/controlroom/internal/demo -update")
	}
}

func TestSnapshotContainsRequiredStory(t *testing.T) {
	snapshot := demo.Snapshot()
	want := map[string]bool{
		"run.retry_loop": false, "run.stalled_active": false, "pr.ci_failed": false,
		"pr.review_needed": false, "task.blocked_no_path": false, "task.ready": false,
		"tool.accumulated_friction": false, "source.stale": false, "source.unavailable": false,
	}
	for _, item := range snapshot.Attention {
		if _, ok := want[item.RuleID]; ok {
			want[item.RuleID] = true
		}
	}
	for rule, found := range want {
		if !found {
			t.Errorf("demo missing %s", rule)
		}
	}
	if len(snapshot.Reliability) == 0 || snapshot.Reliability[0].InputTokens.State != "unknown" {
		t.Fatal("demo must include diagnosis with unknown telemetry")
	}
}
