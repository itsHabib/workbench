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

func TestRelativeStateCannotLaunchProviderInAnotherDirectory(t *testing.T) {
	home, _ := deliverEnv(t)
	original := providerCommand
	providerCommand = func(map[string]any) (*exec.Cmd, error) {
		t.Fatal("relative state must be refused before invoking a provider")
		return nil, nil
	}
	t.Cleanup(func() { providerCommand = original })
	state, org := fleet.State, fleet.OrgState
	defer func() { fleet.State, fleet.OrgState = state, org }()
	for _, root := range []string{"fleet", "org"} {
		fleet.State, fleet.OrgState = state, org
		if root == "fleet" {
			fleet.State = "relative-fleet"
		}
		if root == "org" {
			fleet.OrgState = "relative-org"
		}
		_, err := run(deliverTarget{address: "hub:lead", cwd: home, provider: "claude"}, "test", "", fleet.Now())
		if err == nil || !strings.Contains(err.Error(), "absolute FLEET_STATE and ORG_STATE") {
			t.Fatalf("relative %s root: %v", root, err)
		}
	}
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

func TestLaunchIdentityRejectsRecycledPID(t *testing.T) {
	identity, err := processIdentity(os.Getpid())
	if err != nil || identity == "" {
		t.Fatalf("current process identity: %q %v", identity, err)
	}
	r := fleet.Rec{"status": "running", "pid": os.Getpid(), "process_identity": identity}
	if !launchPresent(r) {
		t.Fatal("current process lost")
	}
	r["process_identity"] = "another-process"
	if launchPresent(r) {
		t.Fatal("recycled PID blocks launch")
	}
	if state, _ := processState(r); state != "gone_exit_unknown" {
		t.Fatal(state)
	}
	delete(r, "process_identity")
	if state, _ := processState(r); state != "unknown" {
		t.Fatal("unproven identity reported as running", state)
	}
}

func TestAddressStopPausesDetachedLead(t *testing.T) {
	home, sink := deliverEnv(t)
	key, err := fleet.MailStopKey("hub:lead")
	if err != nil {
		t.Fatal(err)
	}
	if err := fleet.WriteJSON(fleet.KeyFile("stop", key), fleet.Rec{"key": key, "reason": "run complete"}); err != nil {
		t.Fatal(err)
	}
	config := fleet.ReadJSON(fleet.Path("deliver.json"))
	target := fleet.M(config, "hub:lead")
	target["every"], target["prompt"] = "1s", "advance the run"
	if err := fleet.WriteJSON(fleet.Path("deliver.json"), config); err != nil {
		t.Fatal(err)
	}
	if obs := observedWhat(deliver(fleet.Now()), "mail-delivery-started"); len(obs) != 0 {
		t.Fatal(obs)
	}
	if row := runtimeRow(deliverTarget{address: "hub:lead", cwd: home}, nil); !fleet.B(row, "starts_paused") {
		t.Fatal(row)
	}
	fleet.Unlink(fleet.KeyFile("stop", key))
	deliver(fleet.Now())
	launched(t, sink)
}

func TestResumeBoundToWorkProviderAndAttempt(t *testing.T) {
	home, _ := deliverEnv(t)
	target := deliverTarget{address: "hub:lead", cwd: home, provider: "codex"}
	stateFile := filepath.Join(t.TempDir(), "state.json")
	state := fleet.Rec{"provider": "codex", "attempt": "one", "provider_session": "actual-session"}
	if err := fleet.WriteJSON(stateFile, state); err != nil {
		t.Fatal(err)
	}
	last := fleet.Rec{"provider": "codex", "attempt": "one", "work_identity": workIdentity(target), "state_file": stateFile}
	if got, err := resumeSession(target, last); err != nil || got != "actual-session" {
		t.Fatal(got, err)
	}
	last["resume"] = "different-session"
	if _, err := resumeSession(target, last); err == nil {
		t.Fatal("accepted a changed resumed session")
	}
	delete(last, "resume")
	last["attempt"] = "replacement"
	if _, err := resumeSession(target, last); err == nil {
		t.Fatal("resumed stale attempt evidence")
	}
	target.fresh = true
	if got, err := resumeSession(target, last); err != nil || got != "" {
		t.Fatal(got, err)
	}
	target.fresh = false
	last["work_identity"] = "different-work"
	if got, err := resumeSession(target, last); err != nil || got != "" {
		t.Fatal(got, err)
	}
	last["work_identity"] = workIdentity(target)
	last["provider"] = "claude"
	if got, err := resumeSession(target, last); err != nil || got != "" {
		t.Fatal(got, err)
	}
}

func TestCancelUsesExactAttemptFile(t *testing.T) {
	home, _ := deliverEnv(t)
	target := deliverTarget{address: "hub:lead", cwd: home}
	identity, err := processIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	cancel := filepath.Join(t.TempDir(), "attempt.cancel")
	launch := fleet.Rec{"address": "hub:lead", "cwd": home, "at": fleet.Now(), "status": "running", "pid": os.Getpid(), "process_identity": identity, "attempt": "one", "cancel_file": cancel}
	if err := fleet.WriteJSON(launchPath(target), launch); err != nil {
		t.Fatal(err)
	}
	if err := Cancel("hub:lead"); err != nil {
		t.Fatal(err)
	}
	if got := fleet.ReadJSON(cancel); fleet.S(got, "attempt") != "one" {
		t.Fatal(got)
	}
	if err := os.Remove(fleet.Path("deliver.json")); err != nil {
		t.Fatal(err)
	}
	if err := Cancel("hub:lead"); err != nil {
		t.Fatalf("removing config lost cancellation: %v", err)
	}
	launch["process_identity"] = "recycled"
	if err := fleet.WriteJSON(launchPath(target), launch); err != nil {
		t.Fatal(err)
	}
	if err := Cancel("hub:lead"); err == nil {
		t.Fatal("cancelled an unverified PID")
	}
	launch["status"] = "failed" // fixture cleanup must not wait for this test process
	if err := fleet.WriteJSON(launchPath(target), launch); err != nil {
		t.Fatal(err)
	}
}

func TestProviderWithoutCollectedExitKeepsReservation(t *testing.T) {
	r := fleet.Rec{"status": "running", "provider": "codex", "pid": os.Getpid(), "process_identity": "previous-process"}
	if !launchPresent(r) {
		t.Fatal("absent bridge allowed replacement despite unknown provider descendants")
	}
}

func TestWatcherLossUsesMatchingProviderEvidence(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "state.json")
	launch := fleet.Rec{"provider": "codex", "attempt": "one", "status": "running", "pid": os.Getpid(), "process_identity": "previous-process", "state_file": stateFile}
	if state, _ := processState(launch); state != "gone_exit_unknown" {
		t.Fatalf("fixture must have no collected bridge exit: %s", state)
	}
	for _, state := range []fleet.Rec{
		{"provider": "codex", "attempt": "one", "provider_started": true, "provider_terminal": true},
		{"provider": "codex", "attempt": "one", "provider_started": false, "provider_terminal": false},
	} {
		if err := fleet.WriteJSON(stateFile, state); err != nil {
			t.Fatal(err)
		}
		if launchPresent(launch) {
			t.Fatalf("lost watcher retained a finished or never-started provider: %v", state)
		}
		identity, err := processIdentity(os.Getpid())
		if err != nil {
			t.Fatal(err)
		}
		launch["process_identity"] = identity
		if !launchPresent(launch) {
			t.Fatal("terminal evidence released a live bridge")
		}
		launch["process_identity"] = "previous-process"
		state["attempt"] = "old"
		if err := fleet.WriteJSON(stateFile, state); err != nil {
			t.Fatal(err)
		}
		if !launchPresent(launch) {
			t.Fatal("stale evidence released the reservation")
		}
	}
}

func TestNeverStartedProviderRetriesRequestedSession(t *testing.T) {
	home, _ := deliverEnv(t)
	target := deliverTarget{address: "hub:lead", cwd: home, provider: "claude"}
	stateFile := filepath.Join(t.TempDir(), "state.json")
	last := fleet.Rec{"provider": "claude", "attempt": "one", "status": "running", "state_file": stateFile, "work_identity": workIdentity(target)}
	if err := fleet.WriteJSON(stateFile, fleet.Rec{"provider": "claude", "attempt": "one", "provider_started": false}); err != nil {
		t.Fatal(err)
	}
	for _, requested := range []string{"", "retained-session"} {
		last["resume"] = requested
		if got, err := resumeSession(target, last); err != nil || got != requested {
			t.Fatalf("never-started attempt lost requested identity: got %q, err %v", got, err)
		}
	}
}

func TestCollectedBridgeExitDoesNotReleaseAnUnfinishedProvider(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "state.json")
	exitFile := filepath.Join(t.TempDir(), "exit.json")
	if err := fleet.WriteJSON(exitFile, fleet.Rec{"exit_code": 137}); err != nil {
		t.Fatal(err)
	}
	launch := fleet.Rec{"provider": "codex", "attempt": "one", "status": "running", "exit_file": exitFile, "state_file": stateFile}
	state := fleet.Rec{"provider": "codex", "attempt": "one", "provider_started": true, "provider_terminal": false}
	if err := fleet.WriteJSON(stateFile, state); err != nil {
		t.Fatal(err)
	}
	if !launchPresent(launch) {
		t.Fatal("a collected killed-bridge exit released the provider reservation")
	}
	state["provider_terminal"] = true
	if err := fleet.WriteJSON(stateFile, state); err != nil {
		t.Fatal(err)
	}
	if launchPresent(launch) {
		t.Fatal("provider terminal result plus collected bridge exit did not release")
	}
	state["attempt"] = "old"
	if err := fleet.WriteJSON(stateFile, state); err != nil {
		t.Fatal(err)
	}
	if !launchPresent(launch) {
		t.Fatal("a stale provider result released the replacement")
	}
}

func TestPreTurnQuiescenceRequiresCompleteBoundEvidence(t *testing.T) {
	home, _ := deliverEnv(t)
	target := deliverTarget{address: "hub:lead", cwd: home, provider: "codex"}
	cases := []struct {
		name    string
		change  func(fleet.Rec, fleet.Rec)
		release bool
	}{
		{"complete", func(fleet.Rec, fleet.Rec) {}, true},
		{"possibly dispatched", func(s, _ fleet.Rec) { s["turn_may_have_been_sent"] = true }, false},
		{"missing phase", func(s, _ fleet.Rec) { delete(s, "turn_may_have_been_sent") }, false},
		{"ambiguous phase", func(s, _ fleet.Rec) { s["turn_may_have_been_sent"] = nil }, false},
		{"fork survived", func(_, p fleet.Rec) { p["fork_observed"] = true }, false},
		{"missing fork observation", func(_, p fleet.Rec) { delete(p, "fork_observed") }, false},
		{"observer unavailable", func(_, p fleet.Rec) { p["armed"] = false }, false},
		{"missing exit", func(_, p fleet.Rec) { delete(p, "exit_observed") }, false},
		{"stale attempt", func(_, p fleet.Rec) { p["attempt"] = "old" }, false},
		{"wrong provider", func(_, p fleet.Rec) { p["provider"] = "claude" }, false},
		{"observer error", func(_, p fleet.Rec) { p["error"] = "lost events" }, false},
		{"missing rejection", func(s, _ fleet.Rec) { delete(s, "pre_turn_rejection") }, false},
		{"fake never started", func(s, _ fleet.Rec) { s["provider_started"] = nil; delete(s, "pre_turn_rejection") }, false},
		{"fake terminal", func(s, _ fleet.Rec) { s["provider_terminal"] = "true"; delete(s, "pre_turn_rejection") }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stateFile := filepath.Join(t.TempDir(), "state.json")
			last := fleet.Rec{"provider": "codex", "attempt": "one", "state_file": stateFile, "status": "running", "pid": os.Getpid(), "process_identity": "previous-process", "resume": "requested", "work_identity": workIdentity(target)}
			state := fleet.Rec{"provider": "codex", "attempt": "one", "provider_started": true, "provider_terminal": false, "provider_quiescent": true, "turn_may_have_been_sent": false, "pre_turn_rejection": "thread/resume"}
			proof := fleet.Rec{"schema": "fleet.process-proof.v1", "method": "darwin-no-fork-v1", "provider": "codex", "attempt": "one", "pid": 123, "armed": true, "exec_observed": true, "exit_observed": true, "fork_observed": false, "quiescent": true}
			tc.change(state, proof)
			if err := fleet.WriteJSON(stateFile, state); err != nil {
				t.Fatal(err)
			}
			if err := fleet.WriteJSON(stateFile+".process.json", proof); err != nil {
				t.Fatal(err)
			}
			if got := !launchPresent(last); got != tc.release {
				t.Fatalf("released=%v want %v", got, tc.release)
			}
			if !tc.release {
				return
			}
			if got, err := resumeSession(target, last); err != nil || got != "requested" {
				t.Fatalf("retry lost requested session: %q %v", got, err)
			}
		})
	}
}

func TestReleaseEndsOnlyAnAbsentUnfinishedReservation(t *testing.T) {
	home, _ := deliverEnv(t)
	target := deliverTarget{address: "hub:lead", cwd: home, provider: "claude"}
	stateFile := filepath.Join(t.TempDir(), "attempt.state.json")
	if err := fleet.WriteJSON(stateFile, fleet.Rec{"attempt": "one", "provider": "claude", "provider_started": true, "provider_terminal": false, "provider_session": "s1"}); err != nil {
		t.Fatal(err)
	}
	// A bridge that vanished mid-turn: pid gone, no exit file, provider never terminal.
	launch := fleet.Rec{"address": "hub:lead", "cwd": home, "at": fleet.Now(), "status": "running", "pid": 999999, "process_identity": "gone", "provider": "claude", "attempt": "one", "state_file": stateFile, "resume": "s1", "work_identity": workIdentity(target)}
	if err := fleet.WriteJSON(launchPath(target), launch); err != nil {
		t.Fatal(err)
	}
	if !launchPresent(launch) {
		t.Fatal("fixture must start reserved")
	}
	if err := Release("hub:lead", ""); err == nil {
		t.Fatal("a release without a reason is not a record")
	}
	if err := Release("hub:lead", "bridge killed; provider confirmed gone"); err != nil {
		t.Fatal(err)
	}
	after, err := readLaunch(target)
	if err != nil {
		t.Fatal(err)
	}
	if launchPresent(after) || fleet.S(after, "status") != "released" || fleet.S(after, "prior_state") != "gone_exit_unknown" {
		t.Fatalf("release did not end the reservation: %v", after)
	}
	if resume, err := resumeSession(target, after); err != nil || resume != "s1" {
		t.Fatalf("the next wake must resume the recorded session, got %q %v", resume, err)
	}
	if err := Release("hub:lead", "again"); err == nil {
		t.Fatal("a released attempt cannot be released twice")
	}
	rows, _ := os.ReadFile(filepath.Join(dir(), "observed.jsonl"))
	if !strings.Contains(string(rows), "delivery-released") || !strings.Contains(string(rows), "provider confirmed gone") {
		t.Fatalf("release must be recorded with its reason: %s", rows)
	}
	// A running attempt is refused; cancel is the verb for that.
	identity, _ := processIdentity(os.Getpid())
	live := fleet.Rec{"address": "hub:lead", "cwd": home, "at": fleet.Now(), "status": "running", "pid": os.Getpid(), "process_identity": identity, "provider": "claude", "attempt": "two", "state_file": stateFile}
	if err := fleet.WriteJSON(launchPath(target), live); err != nil {
		t.Fatal(err)
	}
	if err := Release("hub:lead", "impatient"); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("released a live attempt: %v", err)
	}
	live["status"] = "failed" // fixture cleanup must not wait for this test process
	if err := fleet.WriteJSON(launchPath(target), live); err != nil {
		t.Fatal(err)
	}
}
