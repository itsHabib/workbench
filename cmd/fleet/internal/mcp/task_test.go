package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestTaskStatusMCPDoesNotInitializeOrMigrate(t *testing.T) {
	old := fleet.State
	fleet.State = filepath.Join(t.TempDir(), "absent")
	t.Cleanup(func() { fleet.State = old })
	var out strings.Builder
	Serve(strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"fleet_status\",\"arguments\":{}}}\n"), &out)
	if !strings.Contains(out.String(), "fleet-task-status-v1") || strings.Contains(out.String(), `"isError":true`) {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(fleet.State); !os.IsNotExist(err) {
		t.Fatalf("read initialized state: %v", err)
	}
	_ = fleet.WriteJSON(fleet.Path("stop", "legacy.json"), fleet.Rec{"branch": "legacy", "reason": "keep"})
	out.Reset()
	Serve(strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":2,\"method\":\"tools/call\",\"params\":{\"name\":\"fleet_status\",\"arguments\":{}}}\n"), &out)
	var reply struct {
		Result struct {
			Content []struct{ Text string }
			IsError bool
		}
	}
	if err := json.Unmarshal([]byte(out.String()), &reply); err != nil {
		t.Fatal(err)
	}
	if len(reply.Result.Content) != 1 || !json.Valid([]byte(reply.Result.Content[0].Text)) || !reply.Result.IsError {
		t.Fatal("incomplete status is not a parseable error packet", out.String())
	}
	if !strings.Contains(out.String(), "legacy state needs reconciliation") {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(fleet.Path("stop", "legacy.json")); err != nil {
		t.Fatal("legacy moved", err)
	}
}

func TestTaskEntryMCPRejectsEmptyAndMistypedRequirements(t *testing.T) {
	for _, key := range []string{"as", "head", "requires"} {
		args := map[string]any{"change": "task", "id": "one", "worker": "worker", "for": "lead", "brief": "check", "cwd": t.TempDir(), key: ""}
		if err := requestTool(args); err == nil || !strings.Contains(err.Error(), "non-empty") {
			t.Fatalf("%s silently disabled: %v", key, err)
		}
		args[key] = 7
		if err := checkArguments("fleet_request", args); err == nil {
			t.Fatalf("non-string %s ignored", key)
		}
	}
}
