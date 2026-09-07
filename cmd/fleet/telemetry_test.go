package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestVerdictTelemetryDoesNotStoreInputOrMutateVerdict(t *testing.T) {
	old := fleet.State
	fleet.State = t.TempDir()
	t.Cleanup(func() { fleet.State = old })
	ev := fleet.Rec{"hook_event_name": "PreToolUse", "session_id": "s", "tool_name": "Bash", "cwd": "/x", "tool_input": fleet.Rec{"command": "echo secret-token"}}
	v := &fleet.Verdict{Code: 2, Err: "denied"}
	logVerdict("claude", ev, v, time.Now(), false)
	b, err := os.ReadFile(fleet.Path("events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	r := fleet.ReadJSONBytes(b)
	if strings.Contains(string(b), "secret-token") || len(fleet.S(r, "fingerprint")) != 64 || fleet.F(r, "code") != 2 {
		t.Fatal(string(b))
	}
	if v.Code != 2 || v.Err != "denied" {
		t.Fatal("verdict mutated")
	}
	// A failed log append must not panic or turn denial into success.
	fleet.State = fleet.Path("events.jsonl")
	logVerdict("claude", ev, v, time.Now(), false)
	if v.Code != 2 {
		t.Fatal("logging failure changed verdict")
	}
}
