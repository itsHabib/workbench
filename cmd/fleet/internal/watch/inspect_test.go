package watch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestInspectReadsHandoffAndTraceWithoutWrites(t *testing.T) {
	home, _ := deliverEnv(t)
	for _, args := range [][]string{{"init", "-b", "main"}, {"-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--allow-empty", "-m", "fixture"}} {
		cmd := exec.Command("git", append([]string{"-C", home}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(err, string(out))
		}
	}
	target := deliverTarget{address: "hub:lead", cwd: home}
	output := strings.TrimSuffix(launchPath(target), ".json") + ".log"
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("{\"type\":\"result\",\"result\":\"retained answer\",\"subtype\":\"success\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	branch := fleet.BranchOf(home)
	if err := fleet.WriteJSON(fleet.KeyFile("handoff", fleet.Scope(home, branch)), fleet.Rec{"conclusion": "Waiting on answer", "next": "Keep the dirty draft", "at": fleet.Now()}); err != nil {
		t.Fatal(err)
	}
	got, err := Inspect("hub:lead")
	if err != nil {
		t.Fatal(err)
	}
	if fleet.S(fleet.M(got, "handoff"), "conclusion") != "Waiting on answer" {
		t.Fatal(got)
	}
	if !strings.Contains(string(fleet.DumpJSON(got)), "retained answer") {
		t.Fatal(got)
	}
	if fleet.M(got, "trace")["data"] != nil {
		t.Fatal("raw trace duplicated in inspect")
	}
	if Heartbeat() != nil {
		t.Fatal("inspect scheduled work")
	}
	if _, err := Inspect("unknown:address"); err == nil {
		t.Fatal("unknown address accepted")
	}
}

func TestTraceBoundsWindowAndMarksCoverage(t *testing.T) {
	home, _ := deliverEnv(t)
	path := strings.TrimSuffix(launchPath(deliverTarget{address: "hub:lead", cwd: home}), ".json") + ".log"
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	data := strings.Repeat("{\"type\":\"system\"}\n", traceWindow/10) + "{\"type\":\"result\",\"result\":\"last word\"}\n{\"torn\":"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := Trace("hub:lead")
	if err != nil {
		t.Fatal(err)
	}
	raw := fleet.S(got, "data")
	if !fleet.B(got, "partial") || len(raw) > traceWindow || strings.Contains(raw, "torn") || !strings.Contains(raw, "last word") {
		t.Fatal("invalid bounded trace", got["coverage"], len(raw))
	}
}
