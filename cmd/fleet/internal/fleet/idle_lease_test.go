package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// idleLab is one checkout on feat/work, a fresh store, and a fixed idle window. It
// returns the checkout and the branch's lease key.
func idleLab(t *testing.T) (string, string) {
	t.Helper()
	oldState, oldOrg, oldIdle, oldTakeovers := State, OrgState, IdleS, HookTakeovers
	State, OrgState, IdleS, HookTakeovers, unannounced = t.TempDir(), t.TempDir(), 1800, nil, nil
	t.Cleanup(func() {
		State, OrgState, IdleS, HookTakeovers, unannounced = oldState, oldOrg, oldIdle, oldTakeovers, nil
	})
	checkout := fakeCheckout(t, "rooms", "feat/work")
	return checkout, Scope(checkout, "feat/work")
}

// fakeCheckout is a directory whose .git names branch: enough for the hook, which reads
// the branch from .git without running git.
func fakeCheckout(t *testing.T, name, branch string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/"+branch+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// holdAs publishes a session record for sid and gives it the lease on key, claimed
// claimedAgo seconds ago. The pid is this test process: a harness that answers.
func holdAs(t *testing.T, sid, key string, claimedAgo float64, fields Rec) {
	t.Helper()
	rec := Rec{"session": sid, "pid": float64(os.Getpid()), "pid_kind": "harness", "ended": false}
	for k, v := range fields {
		rec[k] = v
	}
	if err := WriteJSON(Path("sessions", sid+".json"), rec); err != nil {
		t.Fatal(err)
	}
	lease := LeaseRecord(key, sid, S(rec, "role"), "", "claimed on first write")
	lease["since"] = Now() - claimedAgo
	if err := WriteLease(key, lease); err != nil {
		t.Fatal(err)
	}
}

func editEvent(name, sid, checkout string) Event {
	return Event{"hook_event_name": name, "session_id": sid, "cwd": checkout, "tool_name": "Edit",
		"tool_use_id": "tu-" + sid, "tool_input": Rec{"file_path": filepath.Join(checkout, "snapshot.go")}}
}

func promptEvent(sid, cwd string) Event {
	return Event{"hook_event_name": "UserPromptSubmit", "session_id": sid, "cwd": cwd, "prompt": "continue"}
}

func refusesOnlyByPolicy(t *testing.T, reason string) {
	t.Helper()
	for _, banned := range []string{"operator", "fleet revoke"} {
		if strings.Contains(reason, banned) {
			t.Fatalf("a branch refusal must not route a routine handover through the operator (%q):\n%s", banned, reason)
		}
	}
}

// The 2026-09-13 shape: a Codex session out of usage, its turn left open, its last
// event a PreToolUse hours ago, and the desktop app's pid still answering. Its branch
// goes to the next writer, on the record, and both sides are told at their next event.
func TestAnIdleHoldersBranchGoesToTheNextWriter(t *testing.T) {
	checkout, key := idleLab(t)
	const holder, taker = "01a0750a-81dc-7182-92c4-bd0562829e20", "c1a1dea0-0000-4000-8000-000000000001"
	holdAs(t, holder, key, 5*3600, Rec{"cwd": "/Users/mh/dev", "role": "rooms", "last_event_at": Now() - 4*3600,
		"last_event": "PreToolUse", "turn_open": true})

	if v := Run(editEvent("PreToolUse", taker, checkout)); v.Code != 0 {
		t.Fatalf("a quiet holder's branch must go to the next writer:\n%s", v.Err)
	}
	lease := Lease(key)
	took := M(lease, "takeover")
	if S(lease, "session") != taker || S(took, "from") != holder || S(took, "why") != "idle" || F(took, "quiet_s") < 4*3600 {
		t.Fatalf("the lease must record who took it, from whom, why and after how long: %v", lease)
	}
	if !strings.Contains(S(lease, "note"), "idle session 01a0750a") {
		t.Fatalf("the lease note must name the idle takeover: %q", S(lease, "note"))
	}
	if len(HookTakeovers) != 1 || S(HookTakeovers[0], "from") != holder || S(HookTakeovers[0], "why") != "idle" {
		t.Fatalf("the hook's event row must carry the takeover: %v", HookTakeovers)
	}

	if v := Run(editEvent("PostToolUse", taker, checkout)); !strings.Contains(v.Out, "you took branch feat/work from rooms 01a0750a") || !strings.Contains(v.Out, "preserve it") {
		t.Fatalf("the taker must be told whose uncommitted work may be in the tree: %q", v.Out)
	}
	v := Run(promptEvent(holder, "/Users/mh/dev"))
	if !strings.Contains(v.Out, "branch feat/work was taken from you") || !strings.Contains(v.Out, Short(taker)) {
		t.Fatalf("the displaced holder must hear of it at its next event: %q", v.Out)
	}
	if again := Run(promptEvent(holder, "/Users/mh/dev")); strings.Contains(again.Out, "taken from you") {
		t.Fatalf("a takeover is told once: %q", again.Out)
	}

	v = Run(editEvent("PreToolUse", holder, checkout))
	if v.Code != 2 || !strings.Contains(v.Err, "was taken from you") || !strings.Contains(v.Err, "Fleet touched no files") {
		t.Fatalf("the displaced holder's write is refused while the taker is active, naming the takeover:\n%s", v.Err)
	}
	refusesOnlyByPolicy(t, v.Err)
}

// Review of #350: activity on a branch must survive the session's next event elsewhere.
// A holder that ran commands in the branch's checkout a minute ago, then one command
// from another checkout, is still active on the branch; and a write it has just been
// admitted to counts from admission, before its PostToolUse lands.
func TestActivityOnABranchSurvivesAnEventElsewhere(t *testing.T) {
	checkout, key := idleLab(t)
	other := fakeCheckout(t, "main", "main")
	const holder, rival = "a11ce000", "b0b00000"
	holdAs(t, holder, key, 2*3600, Rec{"cwd": checkout, "last_writes": Rec{key: Rec{"key": key, "at": Now() - 40*60}}})
	for _, ev := range []Event{bashEvent(holder, checkout, "go test ./..."), bashEvent(holder, other, "ls")} {
		if v := Run(ev); v.Code != 0 {
			t.Fatalf("holder's own command refused:\n%s", v.Err)
		}
	}
	if state, _ := HolderState(key, Lease(key)); state != HeldLive {
		t.Fatalf("one event from another checkout erased the holder's activity on the branch: %q", state)
	}
	v := Run(editEvent("PreToolUse", rival, checkout))
	if v.Code != 2 || !strings.Contains(v.Err, "active on it") || !strings.Contains(v.Err, "git worktree add <new-dir> -b <new-branch> feat/work") {
		t.Fatalf("a rival must be refused and pointed at a worktree of its own:\n%s", v.Err)
	}
	refusesOnlyByPolicy(t, v.Err)
}

// An admitted write is activity on its branch from its admission, not only once its
// PostToolUse lands: a holder standing elsewhere whose last recorded write here has aged
// out is active again the moment its next write here is cleared.
func TestAnAdmittedWriteIsActivityFromAdmission(t *testing.T) {
	checkout, key := idleLab(t)
	other := fakeCheckout(t, "main", "main")
	const holder = "a11ce000"
	holdAs(t, holder, key, 2*3600, Rec{"cwd": other, "repo": RepoID(other), "branch": "main", "last_event_at": Now(),
		"last_writes": Rec{key: Rec{"key": key, "at": Now() - 2*float64(IdleS)}}})
	if state, _ := HolderState(key, Lease(key)); state != HeldIdle {
		t.Fatalf("fixture: the holder should start idle on the branch, got %q", state)
	}
	if v := Run(bashEvent(holder, other, "git -C "+checkout+" commit -m wip")); v.Code != 0 {
		t.Fatalf("the holder's own commit was refused:\n%s", v.Err)
	}
	if state, _ := HolderState(key, Lease(key)); state != HeldLive {
		t.Fatalf("an admitted write did not count as activity before its PostToolUse: %q", state)
	}
}

// A holder active on its branch is never displaced, and the refusal says when the
// branch would change hands instead of sending the writer to the operator.
func TestAnActiveHolderIsNeverDisplaced(t *testing.T) {
	checkout, key := idleLab(t)
	elsewhere := Scope(checkout, "feat/other")
	cases := map[string]Rec{
		"standing on the branch": {"repo": RepoID(checkout), "branch": "feat/work", "last_event_at": Now() - 60},
		"writing it from elsewhere": {"repo": RepoID(checkout), "branch": "feat/other", "last_event_at": Now() - 60,
			"last_writes": Rec{key: Rec{"key": key, "at": Now() - 120}, elsewhere: Rec{"key": elsewhere, "at": Now() - 60}}},
	}
	for name, fields := range cases {
		t.Run(name, func(t *testing.T) {
			holdAs(t, "a11ce000", key, 3*3600, fields)
			reason := CheckLease(key, "feat/work", "b0b00000", "", checkout)
			if !strings.Contains(reason, "held by a session a11ce000, active on it") || !strings.Contains(reason, "git worktree add <new-dir> -b <new-branch> feat/work") {
				t.Fatalf("an active holder must be refused, naming the way on:\n%s", reason)
			}
			refusesOnlyByPolicy(t, reason)
			if S(Lease(key), "session") != "a11ce000" || len(HookTakeovers) != 0 {
				t.Fatalf("an active holder was displaced: %v %v", Lease(key), HookTakeovers)
			}
		})
	}
}

// Liveness is not activity. A process that answers with no recent turns is idle on the
// branch; so is a session busy on another branch. A record that does not say where
// the session stands keeps every event as activity: missing evidence is never idleness.
func TestALiveProcessWithNoRecentTurnsIsIdle(t *testing.T) {
	checkout, key := idleLab(t)
	cases := []struct {
		name   string
		fields Rec
		want   HeldState
	}{
		{"turn closed, quiet an hour", Rec{"repo": RepoID(checkout), "branch": "feat/work", "turn_open": false, "last_event_at": Now() - 3600}, HeldIdle},
		{"busy on another branch", Rec{"repo": RepoID(checkout), "branch": "feat/other", "turn_open": true, "last_event_at": Now() - 5,
			"last_writes": Rec{key: Rec{"key": key, "at": Now() - 3*3600}}}, HeldIdle},
		{"where it stands unknown", Rec{"cwd": "/Users/mh/dev", "turn_open": true, "last_event_at": Now() - 5}, HeldLive},
		{"on the branch a minute ago, elsewhere now", Rec{"repo": RepoID(checkout), "branch": "feat/other", "last_event_at": Now() - 5,
			"last_seen": Rec{key: Now() - 60}}, HeldLive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			holdAs(t, "a11ce000", key, 3*3600, c.fields)
			state, rec := HolderState(key, Lease(key))
			if !SessionAlive(rec) {
				t.Fatal("the fixture's holder must be a live process")
			}
			if state != c.want {
				t.Fatalf("state %q, want %q", state, c.want)
			}
		})
	}
}

// A machine resource stays a hard lock: a live holder keeps it however quiet it is, and
// a dead holder's resource still needs a person's --takeover.
func TestResourceLeasesStayHard(t *testing.T) {
	_, _ = idleLab(t)
	const key = "slot:vmhost"
	holdAs(t, "a11ce000", key, 9*3600, Rec{"last_event_at": Now() - 8*3600, "turn_open": false})
	if state, _ := HeldByOther(key, "b0b00000"); state != HeldLive {
		t.Fatalf("a quiet live holder of a resource is still live, got %q", state)
	}
	reason := CheckLease(key, key, "b0b00000", "", "")
	if !strings.Contains(reason, "slot:vmhost is held by") || !strings.Contains(reason, "fleet drop slot:vmhost") {
		t.Fatalf("a held resource must be refused, naming the drop:\n%s", reason)
	}
	if S(Lease(key), "session") != "a11ce000" || len(HookTakeovers) != 0 {
		t.Fatalf("a resource changed hands: %v", Lease(key))
	}
	holdAs(t, "a11ce000", key, 9*3600, Rec{"pid": float64(0), "last_event_at": Now() - 8*3600})
	if reason := CheckLease(key, key, "b0b00000", "", ""); !strings.Contains(reason, "--takeover") {
		t.Fatalf("a dead holder's resource still needs --takeover:\n%s", reason)
	}
}

// The Codex adapter unwinds the leases a denied multi-file patch took. Takeovers are
// announced only once the caller is done unwinding, so an unwound one tells nobody —
// not even after the holder's own lease has since been released.
func TestAnUnwoundTakeoverTellsNobody(t *testing.T) {
	_, key := idleLab(t)
	holdAs(t, "a11ce000", key, 3*3600, Rec{"last_event_at": Now() - 3*3600})
	old := Lease(key)
	if reason := CheckLease(key, "feat/work", "b0b00000", "", ""); reason != "" {
		t.Fatal(reason)
	}
	if restored, err := RestoreLease(key, "b0b00000", old); !restored || err != nil {
		t.Fatalf("restore: %v %v", restored, err)
	}
	ForgetTakeover(key)
	AnnounceTakeovers()
	ReleaseLeases("a11ce000")
	if Has(SessionRecord("a11ce000"), "lease_notices") || Has(SessionRecord("b0b00000"), "lease_notices") {
		t.Fatalf("an unwound takeover left a notice: %v", SessionRecord("a11ce000"))
	}
	if lines := leaseNoticeLines("a11ce000", SessionRecord("a11ce000")); len(lines) != 0 {
		t.Fatalf("an unwound takeover was reported: %v", lines)
	}
}
