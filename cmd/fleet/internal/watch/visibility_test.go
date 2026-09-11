package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestRunReportSeparatesUnknownFromReportedZero(t *testing.T) {
	deliverEnv(t)
	d := filepath.Join(dir(), "delivery")
	if err := os.MkdirAll(d, 0700); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{"one.log": `{"type":"result","session_id":"s1","subtype":"success","num_turns":3,"total_cost_usd":0.25}`, "two.log": `{"type":"result","session_id":"s2","subtype":"error_api","num_turns":0,"total_cost_usd":0}`, "three.log": "provider failed before result"} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := fleet.WriteJSON(filepath.Join(d, "one.exit.json"), fleet.Rec{"address": "hub:lead", "at": fleet.Now(), "exit_code": 0}); err != nil {
		t.Fatal(err)
	}
	got := RunReport(fleet.Now() - 60)
	if got["attempt_count"] != 3 || got["cost_known"] != 2 || got["turns_known"] != 2 || fleet.F(got, "reported_cost_usd") != 0.25 || fleet.F(got, "reported_turns") != 3 {
		t.Fatal(got)
	}
	text := RunReportText(got)
	for _, want := range []string{"error_api", "exit_unknown", "unknown", "hub:lead"} {
		if !strings.Contains(text, want) {
			t.Fatal(text)
		}
	}
	if RunReport(fleet.Now() + 60)["attempt_count"] != 0 {
		t.Fatal("ignored time window")
	}
}

func TestAllStatusReadsHooksWhenWatcherIsStale(t *testing.T) {
	home, _ := deliverEnv(t)
	if err := fleet.WriteJSON(fleet.Path("sessions", "status-session.json"), fleet.Rec{"session": "status-session", "cwd": home, "launch_dir": home, "last_event_at": fleet.Now(), "last_event": "PostToolUse", "last_tool": "Edit", "branch": "task", "pid_kind": "harness", "pid": os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	if err := fleet.WriteJSON(filepath.Join(dir(), "heartbeat.json"), fleet.Rec{"pid": os.Getpid(), "at": fleet.Now() - 3600, "interval": 1}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir(), "heartbeat.json"))
	if err != nil {
		t.Fatal(err)
	}
	got := AllStatus()
	if fleet.S(got, "watcher") != "stale" {
		t.Fatal(got)
	}
	rows := got["workers"].([]fleet.Rec)
	if len(rows) != 2 || fleet.S(rows[0], "session") != "status-session" || fleet.S(rows[0], "last_tool") != "Edit" {
		t.Fatal(rows)
	}
	after, err := os.ReadFile(filepath.Join(dir(), "heartbeat.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("status ticked watcher", err)
	}
	if !strings.Contains(AllStatusText(got), "status-session") {
		t.Fatal("session not visible")
	}
}
