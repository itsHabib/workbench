package main

import (
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestStartupHealthPreservesContextAndVerdict(t *testing.T) {
	old := fleet.State
	fleet.State = t.TempDir()
	t.Cleanup(func() { fleet.State = old })
	original := &fleet.Verdict{Code: 0, Out: `{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext":"existing instructions"}}`}
	result := withWatcherHealth(fleet.Rec{"hook_event_name": "SessionStart"}, original, false)
	parsed := fleet.ReadJSONBytes([]byte(result.Out))
	context := fleet.S(fleet.M(parsed, "hookSpecificOutput"), "additionalContext")
	if !strings.Contains(context, "existing instructions") || !strings.Contains(context, "watcher: never_seen") || result.Code != original.Code {
		t.Fatal(result)
	}
	starting := withWatcherHealth(fleet.Rec{"hook_event_name": "SessionStart"}, original, true)
	if !strings.Contains(starting.Out, "revival requested") {
		t.Fatal(starting)
	}
	if strings.Contains(original.Out, "watcher") {
		t.Fatal("mutated shared verdict")
	}
	if got := withWatcherHealth(fleet.Rec{"hook_event_name": "PreToolUse"}, original, false); got != original {
		t.Fatal("changed non-start verdict")
	}
}
