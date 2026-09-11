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
