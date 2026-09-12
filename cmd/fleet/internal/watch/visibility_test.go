package watch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
	"github.com/itsHabib/workbench/cmd/fleet/internal/verbs"
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
	if err := fleet.WriteJSON(filepath.Join(d, "three.meta.json"), fleet.Rec{"address": "hub:missing-exit", "cwd": "/observed", "at": fleet.Now()}); err != nil {
		t.Fatal(err)
	}
	got := RunReport(fleet.Now() - 60)
	if got["attempt_count"] != 3 || got["cost_known"] != 2 || got["turns_known"] != 2 || fleet.F(got, "reported_cost_usd") != 0.25 || fleet.F(got, "reported_turns") != 3 {
		t.Fatal(got)
	}
	text := RunReportText(got)
	for _, want := range []string{"error_api", "exit_unknown", "unknown", "hub:lead", "hub:missing-exit"} {
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
	if err := fleet.WriteJSON(filepath.Join(dir(), "heartbeat.json"), fleet.Rec{"pid": os.Getpid(), "at": fleet.Now() - 3600, "interval": 1, "notification_configured": true}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir(), "heartbeat.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLEET_NOTIFY", "")
	got := AllStatus()
	if !fleet.B(got, "notification_configured") {
		t.Fatal("read observer environment instead of watcher", got)
	}
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

func TestAllStatusIncludesNestedUnconfiguredOccupant(t *testing.T) {
	home, _ := deliverEnv(t)
	nested := filepath.Join(home, "seat", "subdir")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := fleet.WriteJSON(fleet.Path("sessions", "nested.json"), fleet.Rec{"session": "nested", "cwd": nested, "launch_dir": nested, "last_event_at": fleet.Now(), "last_event": "PostToolUse", "last_tool": "Edit", "pid_kind": "harness", "pid": os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	for _, row := range AllStatus()["workers"].([]fleet.Rec) {
		if fleet.S(row, "slot") != "seat-1" {
			continue
		}
		if fleet.S(row, "session") != "nested" || fleet.S(row, "state") != "observed_session" || fleet.S(row, "last_tool") != "Edit" {
			t.Fatal(row)
		}
		return
	}
	t.Fatal("missing seat")
}

func TestStatusRejectsStaleAssignment(t *testing.T) {
	home, _ := deliverEnv(t)
	for _, args := range [][]string{{"init", "-b", "task"}, {"remote", "add", "origin", "https://github.com/example/demo.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", home}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%s: %v", out, err)
		}
	}
	row := fleet.Rec{"cwd": home, "slot": "seat-1", "role": "hub:b", "tenant": "t1", "branch": "task"}
	a := fleet.Rec{"path": home, "slot": "seat-1", "role": "hub:b", "tenant": "t1", "branch": "task", "repo": fleet.RepoID(home)}
	if !currentAssignment(row, a) {
		t.Fatal("rejected current assignment", a)
	}
	for _, key := range []string{"path", "slot", "role", "tenant", "branch", "repo"} {
		old := a[key]
		a[key] = "stale"
		if currentAssignment(row, a) {
			t.Fatal("accepted stale", key)
		}
		a[key] = old
	}
}

func TestRunReportUnknownTotalsAndFields(t *testing.T) {
	deliverEnv(t)
	d := filepath.Join(dir(), "delivery")
	if err := os.MkdirAll(d, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "unknown.log"), []byte(`{"type":"result"}`), 0600); err != nil {
		t.Fatal(err)
	}
	r := RunReport(0)
	if r["reported_cost_usd"] != nil || r["reported_turns"] != nil {
		t.Fatal(r)
	}
	text := RunReportText(r)
	for _, want := range []string{"cost unknown", "turns unknown", "session unknown", "exit unknown"} {
		if !strings.Contains(text, want) {
			t.Fatal(text)
		}
	}
	if strings.Contains(text, "<nil>") || strings.Contains(text, "$0.0000") {
		t.Fatal(text)
	}
}

func TestLastResultAtWindowBoundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boundary.log")
	result := `{"type":"result","num_turns":3}` + "\n"
	data := "prefix\n" + result + strings.Repeat(" ", int(traceWindow)-len(result))
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if fleet.F(lastResult(path), "num_turns") != 3 {
		t.Fatal("lost complete boundary result")
	}
	data = "prefixX" + result + strings.Repeat(" ", int(traceWindow)-len(result))
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if lastResult(path) != nil {
		t.Fatal("accepted partial boundary record")
	}
}

func TestStatusRejectsPreviousBindingLaunch(t *testing.T) {
	home, _ := deliverEnv(t)
	target := deliverTarget{address: "hub:lead", cwd: home}
	t.Cleanup(func() { _ = os.Remove(launchPath(target)) }) // synthetic record, no child to collect
	for _, identity := range []fleet.Rec{{"address": "old-address", "cwd": home}, {"address": "hub:lead", "cwd": filepath.Join(home, "other")}} {
		rec := fleet.Rec{"at": fleet.Now(), "status": "running", "address": identity["address"], "cwd": identity["cwd"], "pid": os.Getpid(), "output": "old-worker.log"}
		if err := fleet.WriteJSON(launchPath(target), rec); err != nil {
			t.Fatal(err)
		}
		row := runtimeRow(target, nil)
		if fleet.S(row, "state") != "unknown" || !strings.Contains(fleet.S(row, "error"), "another binding") {
			t.Fatal(row)
		}
		for _, key := range []string{"pid", "output", "result", "exit_code", "session"} {
			if fleet.Has(row, key) {
				t.Fatalf("attached old %s: %v", key, row)
			}
		}
		// Display rejection must not change directory-level launch exclusion.
		retained, err := readLaunch(target)
		if err != nil || fleet.F(retained, "pid") != float64(os.Getpid()) {
			t.Fatal(retained, err)
		}
	}
}

func TestStatusMissingCheckoutDoesNotJoinParent(t *testing.T) {
	home, _ := deliverEnv(t)
	if out, err := exec.Command("git", "-C", home, "init", "-b", "parent").CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	missing := filepath.Join(home, "deleted")
	row := fleet.Rec{"cwd": missing, "slot": "seat-1", "branch": "stale"}
	work := []verbs.WorkRow{{"slot": "seat-1", "repo": fleet.RepoID(home), "change": "parent"}}
	enrichStatus(row, work)
	if fleet.S(row, "branch") != "" || fleet.M(row, "assignment") != nil || len(row["work"].([]verbs.WorkRow)) != 0 || fleet.S(row, "head_error") == "" {
		t.Fatal(row)
	}
}
