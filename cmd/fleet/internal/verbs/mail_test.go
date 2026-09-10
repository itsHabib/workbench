package verbs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestMailVerbsIdentityScopeAndUsage(t *testing.T) {
	repo, sid := requestFixture(t)
	other := t.TempDir()
	_ = os.MkdirAll(fleet.OrgState, 0700)
	_ = os.WriteFile(fleet.RolesMap(), []byte(fmt.Sprintf("%s t hub:a\n%s t hub:b\n", repo, other)), 0600)
	args := []string{"send", "hub:b", "--id", "m1", "--kind", "question", "--subject", "--literal", "--body", "hello", "--session", sid[:8]}
	if err := Dispatch(args); err != nil {
		t.Fatal(err)
	}
	path := fleet.MailPath("hub:b", "m1")
	before, _ := os.ReadFile(path)
	if err := Dispatch(args); err != nil {
		t.Fatal(err)
	}
	if err := Dispatch(args); err != nil {
		t.Fatal("retained retry failed", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("retry rewrote mail")
	}
	if err := CmdAck("m1", sid); err == nil {
		t.Fatal("sender acked recipient's mail")
	}
	if err := CmdSend("hub:stranger", "m2", "question", "hi", "", "hello", sid); err == nil || !strings.Contains(err.Error(), "not bound") {
		t.Fatal(err)
	}
	for _, bad := range [][]string{{"send"}, {"send", "hub:b", "--kind", "invalid"}, {"mail", "--oops"}, {"mail", "extra"}, {"ack"}, {"ack", "m1", "extra"}} {
		var refusal *Refusal
		err := Dispatch(bad)
		if !errors.As(err, &refusal) || refusal.Code != 2 {
			t.Fatalf("%v: %v", bad, err)
		}
	}
	if err := CmdSend("hub:b", "m2", "question", "hi", "", "hello", "foreign"); err == nil {
		t.Fatal("borrowed session")
	}
	_ = fleet.WriteJSON(fleet.Path("sessions", "recipient.json"), fleet.Rec{"session": "recipient", "cwd": other, "role": "hub:b", "last_event_at": fleet.Now()})
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	if err := CmdAck("m1", "recipient"); err != nil {
		t.Fatal(err)
	}
	r := fleet.ReadJSON(path)
	if fleet.S(r, "acked_by") != "recipient" {
		t.Fatal(r)
	}
	var buf strings.Builder
	Out = &buf
	if err := CmdMail("", "recipient", true, true); err != nil || strings.TrimSpace(buf.String()) != "[]" {
		t.Fatal(err, buf.String())
	}
	if _, err := os.Stat(fleet.Path("migrated-keys.v1")); !os.IsNotExist(err) {
		t.Fatal("mail initialized legacy migration", err)
	}
}

func TestMailReplacementSenderAndFlatSeatAccess(t *testing.T) {
	repo, sid := requestFixture(t)
	peer, foreign := t.TempDir(), t.TempDir()
	_ = os.MkdirAll(fleet.OrgState, 0700)
	_ = os.WriteFile(fleet.RolesMap(), []byte(fmt.Sprintf("%s one worker:a seat-a\n%s one hub:b\n%s two hub:z\n", repo, peer, foreign)), 0600)
	if err := CmdSend("hub:b", "stable", "report", "result", "abc123", "full\nbody", sid); err != nil {
		t.Fatal("undispatched seat could not message peer", err)
	}
	path := fleet.MailPath("hub:b", "stable")
	before, _ := os.ReadFile(path)
	original := fleet.SessionRecord(sid)
	original["ended"] = true
	_ = fleet.WriteJSON(fleet.Path("sessions", sid+".json"), original)
	replacement := fleet.Rec{"session": "replacement", "cwd": repo, "last_event_at": fleet.Now()}
	_ = fleet.WriteJSON(fleet.Path("sessions", "replacement.json"), replacement)
	_ = os.Remove(fleet.Path("sessions", sid+".json"))
	if err := CmdSend("hub:b", "stable", "report", "result", "abc123", "full\nbody", ""); err != nil {
		t.Fatal("replacement sender retry failed", err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("replacement rewrote original provenance or payload")
	}
	if err := CmdSend("hub:z", "no", "report", "result", "", "body", ""); err == nil {
		t.Fatal("cross-tenant send accepted")
	}
	if err := CmdMail("hub:z", "", false, true); err == nil {
		t.Fatal("cross-tenant read accepted")
	}
	if _, err := os.Stat(fleet.Path("dispatch")); !os.IsNotExist(err) {
		t.Fatal("mail created assignment", err)
	}
	if _, err := os.Stat(fleet.Path("leases")); !os.IsNotExist(err) {
		t.Fatal("mail acquired authority", err)
	}
}

func TestMailSubjectUsageLimit(t *testing.T) {
	var refusal *Refusal
	err := CmdSend("hub:b", "large", "report", strings.Repeat("a", fleet.MaxMailSubjectBytes+1), "", "body", "")
	if !errors.As(err, &refusal) || refusal.Code != 2 {
		t.Fatal("subject limit is not usage", err)
	}
}
