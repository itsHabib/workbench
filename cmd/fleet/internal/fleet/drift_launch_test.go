package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// launchDriftLab lays out the headless lab the defect was observed in: a verifier and
// an author checkout, each bound to its own role, and the run's result tree beside
// them, bound to nobody.
func launchDriftLab(t *testing.T) (verifier, author, logs string) {
	t.Helper()
	oldState, oldOrg := State, OrgState
	State, OrgState = t.TempDir(), t.TempDir()
	t.Cleanup(func() { State, OrgState = oldState, oldOrg })
	lab := t.TempDir()
	verifier = filepath.Join(lab, "workbench-verifier")
	author = filepath.Join(lab, "workbench-author-1")
	logs = filepath.Join(lab, "result", "rooms", "out", "logs")
	for _, d := range []string{filepath.Join(verifier, "cmd", "fleet"), author, logs} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	text := verifier + " headless-lab verifier:headless\n" + author + " headless-lab author:workbench workbench-author-1\n"
	if err := os.WriteFile(RolesMap(), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return verifier, author, logs
}

func bashEvent(sid, cwd, cmd string) Event {
	return Event{"hook_event_name": "PreToolUse", "session_id": sid, "cwd": cwd, "tool_name": "Bash", "tool_input": Rec{"command": cmd}}
}

// Observed 2026-09-13 in a headless lab: a verifier launched in its bound checkout ran
// `cd <lab>/result/rooms/out/logs`, the harness kept that cwd for later calls, and from
// then on the guard refused `cd <its own checkout>` — it read "own" from the log
// directory, which is bound to nobody — so the session could not get home to record
// its receipt.
func TestADriftedSessionMayAlwaysReturnToItsLaunchDirectory(t *testing.T) {
	verifier, author, logs := launchDriftLab(t)
	const sid = "7addc21d-0000-4000-8000-000000000000"
	Run(Event{"hook_event_name": "SessionStart", "session_id": sid, "cwd": verifier})
	if rec := SessionRecord(sid); S(rec, "launch_dir") != verifier || S(rec, "role") != "verifier:headless" {
		t.Fatalf("the session was not launched in its bound checkout: %v", rec)
	}
	if v := Run(bashEvent(sid, verifier, "cd "+logs+" && ls -la")); v.Code != 0 {
		t.Fatalf("stepping into an unbound directory must be allowed:\n%s", v.Err)
	}
	// The harness keeps the cd: every event from here on reports the log directory.
	for _, cmd := range []string{
		"cd " + verifier + ` && fleet receipt b288e38 rooms pass "patch matches"`,
		"cd ../../../../workbench-verifier && pwd",
		"cd " + filepath.Join(verifier, "cmd", "fleet") + " && pwd",
	} {
		if v := Run(bashEvent(sid, logs, cmd)); v.Code != 0 {
			t.Fatalf("returning to its own checkout must be allowed, `%s`:\n%s", cmd, v.Err)
		}
	}
	v := Run(bashEvent(sid, logs, "cd "+author+" && git status"))
	if v.Code != 2 {
		t.Fatalf("a drifted session must still be refused another role's checkout: %+v", v)
	}
	for _, want := range []string{
		"`cd " + author + "` would move this session into workbench-author-1 (author:workbench), and the launch directory is what decides a session's identity",
		"this session's own directory is " + LongPath(verifier),
	} {
		if !strings.Contains(v.Err, want) {
			t.Fatalf("refusal does not carry %q:\n%s", want, v.Err)
		}
	}
}

// Where the shell stands does not make a tree this session's: standing in another
// seat, it may still only go home, and moving deeper into that seat is refused.
func TestOwnIsTheLaunchTreeNotTheTreeTheShellStandsIn(t *testing.T) {
	_, one, two, _ := driftFixture(t)
	if reason := cdDestinations("Bash", "cd "+one, two, one); reason != "" {
		t.Fatalf("a session standing in another seat must be allowed home: %s", reason)
	}
	reason := cdDestinations("Bash", "cd src", two, one)
	if !strings.Contains(reason, "bench-hand-2") || !strings.Contains(reason, "this session's own directory is "+LongPath(one)) {
		t.Fatalf("moving deeper into another seat must be refused, naming this session's own:\n%s", reason)
	}
}
