package watch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestStatusObservesRunningChildAndFailureWithoutATick(t *testing.T) {
	home, sink := deliverEnv(t)
	stop := filepath.Join(t.TempDir(), "finish")
	t.Setenv("FLEET_TEST_WAIT_FILE", stop)
	t.Setenv("FLEET_TEST_EXIT_CODE", "7")
	t.Cleanup(func() { _ = os.WriteFile(stop, nil, 0600) })
	putStoreMail(t, "hub:lead", "status", fleet.Now()-60, nil)
	deliver(fleet.Now())
	launched(t, sink)
	target := deliverTarget{address: "hub:lead", cwd: home}
	row := runtimeRow(target, []fleet.Rec{{"session": "worker", "launch_dir": home, "last_event": "PreToolUse", "last_tool": "Bash", "last_event_at": fleet.Now()}})
	if row["state"] != "running" || row["last_tool"] != "Bash" || row["session"] != "worker" {
		t.Fatal(row)
	}
	if Heartbeat() != nil {
		t.Fatal("status scheduled a watcher tick")
	}
	if err := os.WriteFile(stop, nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitExit(t, target)
	row = runtimeRow(target, nil)
	if row["state"] != "failed" || fleet.F(row, "exit_code") != 7 || fleet.S(row, "error") == "" {
		t.Fatal(row)
	}
	for _, want := range []string{"hub:lead: failed", "exit 7", "Output:", "Trace:"} {
		if out := RuntimeText(); !strings.Contains(out, want) {
			t.Fatalf("status lacks %q: %s", want, out)
		}
	}
}

func TestStatusDoesNotInventActivityOrHideBrokenState(t *testing.T) {
	home, _ := deliverEnv(t)
	target := deliverTarget{address: "hub:lead", cwd: home, configError: "invalid recurrence"}
	row := runtimeRow(target, nil)
	if row["state"] != "not_started" || row["configuration_error"] != "invalid recurrence" || fleet.Has(row, "last_event_at") {
		t.Fatal(row)
	}
	if err := fleet.WriteJSON(launchPath(target), fleet.Rec{"at": fleet.Now(), "status": "starting"}); err != nil {
		t.Fatal(err)
	}
	row = runtimeRow(target, nil)
	if row["state"] != "unknown" || !strings.Contains(fleet.S(row, "error"), "unresolved") {
		t.Fatal(row)
	}
}
