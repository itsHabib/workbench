package verbs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestReceiptDoesNotRequireRoleOrProducingLane(t *testing.T) {
	for _, role := range []string{"", "supervisor:run"} {
		t.Run(role, func(t *testing.T) {
			repo, sid := requestFixture(t)
			session := fleet.SessionRecord(sid)
			if role != "" {
				session["role"] = role
				session["lane"] = fleet.Rec{"kind": "supervisor", "produces": nil}
				if err := fleet.WriteJSON(fleet.Path("sessions", sid+".json"), session); err != nil {
					t.Fatal(err)
				}
			}
			head, err := gitOut("rev-parse", "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			head = strings.TrimSpace(head)
			if err := cmdReceipt(head, "verify", "pass", "exact-head checks passed", sid, "", false); err != nil {
				t.Fatal(err)
			}
			r := fleet.ReadJSON(fleet.Path("receipts", head+".verify.json"))
			if fleet.S(r, "session") != sid || fleet.S(r, "head") != head || fleet.S(r, "cwd") != canon(repo) || fleet.S(r, "worktree") != canon(repo) || fleet.S(r, "role") != role {
				t.Fatalf("lost provenance: %v", r)
			}
			if role != "" && fleet.S(fleet.M(r, "lane"), "kind") != "supervisor" {
				t.Fatalf("lost lane: %v", r)
			}
			if !doneVerdict(head, "verify").ok {
				t.Fatal("unseated receipt did not satisfy explicit kind")
			}
		})
	}
}

func TestUnseatedReceiptStillRequiresExactCleanHeadAndLiveSession(t *testing.T) {
	repo, sid := requestFixture(t)
	head, err := gitOut("rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	head = strings.TrimSpace(head)
	if err := cmdReceipt("deadbeef", "verify", "pass", "checks", sid, "", false); err == nil {
		t.Fatal("accepted wrong head")
	}
	dirty := filepath.Join(repo, "scratch")
	if err := os.WriteFile(dirty, []byte("unfinished"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := cmdReceipt(head, "verify", "pass", "checks", sid, "", false); err == nil {
		t.Fatal("accepted dirty tree")
	}
	if err := os.Remove(dirty); err != nil {
		t.Fatal(err)
	}
	rec := fleet.SessionRecord(sid)
	rec["ended"] = true
	if err := fleet.WriteJSON(fleet.Path("sessions", sid+".json"), rec); err != nil {
		t.Fatal(err)
	}
	if err := cmdReceipt(head, "verify", "pass", "checks", sid, "", false); err == nil {
		t.Fatal("accepted ended session")
	}
	if fleet.ReadJSON(fleet.Path("receipts", head+".verify.json")) != nil {
		t.Fatal("refusal published receipt")
	}
}

// The directory guard's escape for a session whose shell has wandered off is
// `(cd <its checkout> && fleet receipt …)`. The record names where the shell stands,
// so that receipt was refused — with and without --session — in the session's own
// launch checkout. Observed 2026-09-13: a headless verifier's shell stood in the lab's
// unbound result/rooms/out/logs.
func TestReceiptFromTheLaunchCheckoutWhileTheShellStandsElsewhere(t *testing.T) {
	repo, sid := requestFixture(t)
	logs := filepath.Join(filepath.Dir(repo), "result", "rooms", "out", "logs")
	if err := os.MkdirAll(logs, 0o700); err != nil {
		t.Fatal(err)
	}
	place := func(launch string) {
		rec := fleet.SessionRecord(sid)
		rec["launch_dir"], rec["cwd"] = launch, logs
		if err := fleet.WriteJSON(fleet.Path("sessions", sid+".json"), rec); err != nil {
			t.Fatal(err)
		}
	}
	head, err := gitOut("rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	head = strings.TrimSpace(head)
	place(repo)
	for _, session := range []string{sid, ""} {
		if err := cmdReceipt(head, "rooms", "pass", "patch matches", session, "", false); err != nil {
			t.Fatalf("--session %q in its own launch checkout: %v", session, err)
		}
		r := fleet.ReadJSON(fleet.Path("receipts", head+".rooms.json"))
		if fleet.S(r, "session") != sid || fleet.S(r, "cwd") != canon(repo) {
			t.Fatalf("receipt lost its provenance: %v", r)
		}
	}
	// Neither launched here nor standing here: not this session's directory.
	place(logs)
	for session, why := range map[string]string{sid: "--session only disambiguates", "": "no live session is recorded at"} {
		err := cmdReceipt(head, "rooms", "fail", "borrowed", session, "", false)
		if err == nil || !strings.Contains(err.Error(), why) {
			t.Fatalf("--session %q borrowed a directory the session neither stands in nor was launched in: %v", session, err)
		}
	}
}

// Two sessions launched in one checkout: the one standing in it is the caller, even
// when the other, whose shell has wandered off, acted more recently. Matching both
// fields at once handed the caller's receipt, mail and leases to the other session.
func TestTheSessionStandingHereOutranksOneOnlyLaunchedHere(t *testing.T) {
	repo, here := requestFixture(t)
	rec := fleet.SessionRecord(here)
	rec["launch_dir"], rec["last_event_at"] = repo, fleet.Now()-10
	if err := fleet.WriteJSON(fleet.Path("sessions", here+".json"), rec); err != nil {
		t.Fatal(err)
	}
	const away = "22222222-2222-2222-2222-222222222222"
	logs := filepath.Join(filepath.Dir(repo), "result", "rooms", "out", "logs")
	if err := fleet.WriteJSON(fleet.Path("sessions", away+".json"), fleet.Rec{
		"session": away, "launch_dir": repo, "cwd": logs, "last_event_at": fleet.Now(), "pid_kind": "harness", "pid": os.Getpid(),
	}); err != nil {
		t.Fatal(err)
	}
	if sid, err := currentSession(""); err != nil || sid != here {
		t.Fatalf("resolved %q (%v), want the session standing here, %s", sid, err, fleet.Short(here))
	}
	// The wandered-off session is still named, with where it could run from.
	if sid, err := currentSession(away[:8]); err != nil || sid != away {
		t.Fatalf("its own launch checkout must accept --session: %q %v", sid, err)
	}
	rec["cwd"], rec["launch_dir"] = logs, filepath.Dir(repo)
	if err := fleet.WriteJSON(fleet.Path("sessions", here+".json"), rec); err != nil {
		t.Fatal(err)
	}
	_, err := currentSession(here[:8])
	if err == nil || !strings.Contains(err.Error(), "(launched in "+filepath.Dir(repo)+")") {
		t.Fatalf("the refusal must name the launch directory too: %v", err)
	}
}

func TestReceiptRefusesSessionWithoutRecordedDirectory(t *testing.T) {
	_, sid := requestFixture(t)
	rec := fleet.SessionRecord(sid)
	delete(rec, "cwd")
	if err := fleet.WriteJSON(fleet.Path("sessions", sid+".json"), rec); err != nil {
		t.Fatal(err)
	}
	if err := cmdReceipt("abcdef0", "verify", "pass", "checks", sid, "", false); err == nil {
		t.Fatal("borrowed current directory for an unbound session")
	}
}
