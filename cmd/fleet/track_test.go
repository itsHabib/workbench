package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/codex"
	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestNamedWaitSurvivesTurnsInBothHarnesses(t *testing.T) {
	oldState, oldOrg := fleet.State, fleet.OrgState
	t.Cleanup(func() { fleet.State, fleet.OrgState = oldState, oldOrg })
	for name, run := range map[string]func(fleet.Event) *fleet.Verdict{"claude": fleet.Run, "codex": codex.Run} {
		t.Run(name, func(t *testing.T) {
			fleet.State, fleet.OrgState = t.TempDir(), t.TempDir()
			tracks, cwd := t.TempDir(), t.TempDir()
			t.Setenv("FLEET_TRACK_DIR", tracks)
			anchor := filepath.Join(tracks, "wait.json")
			marker := filepath.Join(tracks, "should-never-run")
			data := fleet.DumpJSON(fleet.Rec{"session": "a", "label": "verdict", "started_at": time.Now().Add(-47 * time.Minute).Format(time.RFC3339Nano),
				"check": "touch " + strconv.Quote(marker)})
			if err := os.WriteFile(anchor, data, 0o600); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				v := run(fleet.Event{"hook_event_name": "UserPromptSubmit", "session_id": "a", "cwd": cwd})
				if v.Code != 0 || !strings.Contains(v.Out, "waiting 47m on:") {
					t.Fatalf("wait lost across turns: %+v", v)
				}
				run(fleet.Event{"hook_event_name": "Stop", "session_id": "a", "cwd": cwd})
			}
			v := run(fleet.Event{"hook_event_name": "UserPromptSubmit", "session_id": "b", "cwd": cwd})
			if strings.Contains(v.Out, "waiting") {
				t.Fatalf("wait leaked to another session: %+v", v)
			}
			after, err := os.ReadFile(anchor)
			if err != nil || string(after) != string(data) {
				t.Fatalf("hook changed the anchor: %v", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("hook ran the optional command: %v", err)
			}
			if err := os.Remove(anchor); err != nil {
				t.Fatal(err)
			}
			v = run(fleet.Event{"hook_event_name": "UserPromptSubmit", "session_id": "a", "cwd": cwd})
			if v.Code != 0 || strings.Contains(v.Out, "waiting") {
				t.Fatalf("stopped wait still injected: %+v", v)
			}
		})
	}
}
