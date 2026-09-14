package fleet

import (
	"os"
	"strings"
	"testing"
)

// sessionStart runs one SessionStart through the hook and returns the record it left.
func sessionStart(t *testing.T, sid, source, cwd string) (Rec, *Verdict) {
	t.Helper()
	ev := Event{"session_id": sid, "cwd": cwd, "hook_event_name": "SessionStart"}
	if source != "" {
		ev["source"] = source
	}
	v := Run(ev)
	if v.Code != 0 {
		t.Fatalf("SessionStart source=%q refused: %+v", source, v)
	}
	return ReadJSON(Path("sessions", sid+".json")), v
}

// A probe on Claude Code 2.1.266: the Bash tool ran `cd <unbound dir>`, then /compact.
// The SessionStart that followed carried the same session id, source "compact" and
// the unbound dir as its cwd. The hook re-recorded launch_dir there and resolved the
// role to none, so the session lost its seat at compaction — and, because the
// directory guard reads "own" from launch_dir, it could no longer cd home.
func TestACompactionInADriftedShellKeepsTheLaunchIdentity(t *testing.T) {
	_, one, _, loose := driftFixture(t)
	rec, _ := sessionStart(t, "sess-a", "startup", one)
	rec["pid"] = float64(0) // so the compact below must be seen re-resolving it
	if err := WriteJSON(Path("sessions", "sess-a.json"), rec); err != nil {
		t.Fatal(err)
	}

	rec, v := sessionStart(t, "sess-a", "compact", loose)
	if got := S(rec, "launch_dir"); got != one {
		t.Fatalf("launch_dir moved with the shell at compaction: %q, want %q", got, one)
	}
	if S(rec, "role") != "hand:bench" || S(rec, "slot") != "bench-hand-1" {
		t.Fatalf("identity lost at compaction: role=%q slot=%q", S(rec, "role"), S(rec, "slot"))
	}
	if !strings.Contains(v.Out, "role hand:bench") {
		t.Fatalf("the context injected after compaction does not name the role:\n%s", v.Out)
	}
	if F(rec, "pid") <= 0 {
		t.Fatal("a compact SessionStart must still re-resolve the harness pid")
	}
	if S(rec, "cwd") != loose {
		t.Fatalf("cwd must still follow the shell: %q", S(rec, "cwd"))
	}
	if v := Run(bashEvent("sess-a", loose, "cd "+one+" && pwd")); v.Code != 0 {
		t.Fatalf("a compacted session must still be able to cd back to its launch directory:\n%s", v.Err)
	}
}

// Only a SessionStart that starts a process re-records where the session lives. A
// clear keeps the launch directory as a compact does; a startup, a resume, or an event
// with no source takes the event's cwd, as every SessionStart did before.
func TestOnlyALaunchReRecordsTheLaunchDirectory(t *testing.T) {
	for _, tc := range []struct {
		source string
		keeps  bool
	}{
		{"compact", true},
		{"clear", true},
		{"startup", false},
		{"resume", false},
		{"", false},
	} {
		t.Run("source="+tc.source, func(t *testing.T) {
			_, one, two, _ := driftFixture(t)
			sessionStart(t, "sess-b", "startup", one)
			rec, _ := sessionStart(t, "sess-b", tc.source, two)
			wantDir, wantSlot := two, "bench-hand-2"
			if tc.keeps {
				wantDir, wantSlot = one, "bench-hand-1"
			}
			if S(rec, "launch_dir") != wantDir || S(rec, "slot") != wantSlot {
				t.Fatalf("launch_dir=%q slot=%q, want %q %q", S(rec, "launch_dir"), S(rec, "slot"), wantDir, wantSlot)
			}
		})
	}
}

// Keeping the launch directory is not keeping the role: a compaction re-reads the map
// for the kept directory, so an operator who deletes its line still strips the role.
func TestACompactionStillReadsTheMapForTheKeptLaunchDirectory(t *testing.T) {
	_, one, two, loose := driftFixture(t)
	sessionStart(t, "sess-c", "startup", one)
	if err := os.WriteFile(RolesMap(), []byte(two+" mh hand:bench bench-hand-2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec, _ := sessionStart(t, "sess-c", "compact", loose)
	if S(rec, "launch_dir") != one {
		t.Fatalf("launch_dir = %q, want %q", S(rec, "launch_dir"), one)
	}
	if S(rec, "role") != "" || S(rec, "slot") != "" {
		t.Fatalf("a role deleted from the map survived compaction: role=%q slot=%q", S(rec, "role"), S(rec, "slot"))
	}
}
