package watch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func waitExit(t *testing.T, target deliverTarget) fleet.Rec {
	t.Helper()
	for i := 0; i < 300; i++ {
		r, err := readLaunch(target)
		if err != nil {
			t.Fatal(err)
		}
		if exit := fleet.ReadJSON(fleet.S(r, "exit_file")); exit != nil {
			return exit
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child exit was not recorded")
	return nil
}

func TestPeriodicWakeWithoutMailUsesOneRuntime(t *testing.T) {
	home, sink := deliverEnv(t)
	cfg := fleet.ReadJSON(fleet.Path("deliver.json"))
	entry := fleet.M(cfg, "hub:lead")
	entry["every"], entry["prompt"] = "2m", "Advance eligible assigned work and report a blocker or result."
	if err := fleet.WriteJSON(fleet.Path("deliver.json"), cfg); err != nil {
		t.Fatal(err)
	}
	now := fleet.Now()
	if got := observedWhat(deliver(now), "mail-delivery-started"); len(got) != 1 {
		t.Fatalf("periodic wake: %v", got)
	}
	if p := launched(t, sink)["prompt"].(string); !strings.Contains(p, "Advance eligible") {
		t.Fatal(p)
	}
	target := deliverTarget{address: "hub:lead", cwd: home}
	if exit := waitExit(t, target); fleet.F(exit, "exit_code") != 0 {
		t.Fatal(exit)
	}
	if got := deliver(now + 119); len(got) != 0 {
		t.Fatalf("early repeat: %v", got)
	}
	if got := observedWhat(deliver(now+121), "mail-delivery-started"); len(got) != 1 {
		t.Fatalf("missed periodic continuation: %v", got)
	}
	waitExit(t, target)
}

func TestNewMailCannotLaunchOverAnUnregisteredChild(t *testing.T) {
	home, sink := deliverEnv(t)
	stop := filepath.Join(t.TempDir(), "finish")
	t.Setenv("FLEET_TEST_WAIT_FILE", stop)
	t.Cleanup(func() { _ = os.WriteFile(stop, nil, 0600) })
	putStoreMail(t, "hub:lead", "first", fleet.Now()-60, nil)
	if got := observedWhat(deliver(fleet.Now()), "mail-delivery-started"); len(got) != 1 {
		t.Fatal(got)
	}
	launched(t, sink)
	putStoreMail(t, "hub:lead", "second", fleet.Now()-60, nil)
	if got := observedWhat(deliver(fleet.Now()), "mail-delivery-started"); len(got) != 0 {
		t.Fatalf("launched a second process before the first emitted hooks: %v", got)
	}
	if err := os.WriteFile(stop, nil, 0600); err != nil {
		t.Fatal(err)
	}
	target := deliverTarget{address: "hub:lead", cwd: home}
	waitExit(t, target)
	if got := observedWhat(deliver(fleet.Now()), "mail-delivery-started"); len(got) != 1 {
		t.Fatalf("waiting mail did not get its next turn: %v", got)
	}
	waitExit(t, target)
}

func TestAssignmentWakesWorkerWithoutOrderAndOnlyOnce(t *testing.T) {
	home, sink := deliverEnv(t)
	seat := filepath.Join(home, "seat")
	for _, args := range [][]string{{"init", "-b", "task"}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture"}} {
		cmd := exec.Command("git", append([]string{"-C", seat}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %s %v", out, err)
		}
	}
	cfg := fleet.ReadJSON(fleet.Path("deliver.json"))
	entry := fleet.M(cfg, "hub:lead")
	entry["cwd"] = seat
	if err := fleet.WriteJSON(fleet.Path("deliver.json"), fleet.Rec{"seat-1": entry}); err != nil {
		t.Fatal(err)
	}
	a := fleet.Rec{"slot": "seat-1", "path": seat, "role": "hub:b", "tenant": "t1", "repo": fleet.RepoID(seat), "branch": "task", "brief": "Implement the task", "at": fleet.Now()}
	if err := fleet.WriteJSON(fleet.Path("assign", "seat-1.json"), a); err != nil {
		t.Fatal(err)
	}
	if got := observedWhat(deliver(fleet.Now()), "mail-delivery-started"); len(got) != 1 {
		t.Fatalf("assigned but not started: %v", got)
	}
	if p := launched(t, sink)["prompt"].(string); !strings.Contains(p, "new assignment") {
		t.Fatal(p)
	}
	waitExit(t, deliverTarget{address: "seat-1", cwd: seat})
	if got := deliver(fleet.Now()); len(got) != 0 {
		t.Fatalf("the same assignment was replayed without permission: %v", got)
	}
}

func TestWatcherCancellationDoesNotWaitForNextInterval(t *testing.T) {
	_, _ = deliverEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if err := serve(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation waited for the next tick")
	}
	b, err := os.ReadFile(filepath.Join(dir(), "observed.jsonl"))
	if err != nil || !strings.Contains(string(b), "watcher-stopped") {
		t.Fatalf("stop was silent: %s %v", b, err)
	}
}

func TestNonzeroChildExitIsRecordedWithoutReplayingMail(t *testing.T) {
	home, _ := deliverEnv(t)
	t.Setenv("FLEET_TEST_EXIT_CODE", "7")
	putStoreMail(t, "hub:lead", "fails", fleet.Now()-60, nil)
	deliver(fleet.Now())
	exit := waitExit(t, deliverTarget{address: "hub:lead", cwd: home})
	if fleet.F(exit, "exit_code") != 7 || fleet.S(exit, "error") == "" {
		t.Fatal("failed child exit was lost", exit)
	}
	if got := deliver(fleet.Now()); len(got) != 0 {
		t.Fatalf("failed work was automatically replayed: %v", got)
	}
}
