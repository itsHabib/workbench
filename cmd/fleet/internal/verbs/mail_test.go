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

func TestMailSeatCommandsCannotBorrowSiblingIdentity(t *testing.T) {
	repo, sid := requestFixture(t)
	sibling, lead := t.TempDir(), t.TempDir()
	_ = os.MkdirAll(fleet.OrgState, 0700)
	_ = os.WriteFile(fleet.RolesMap(), []byte(fmt.Sprintf("%s one worker:demo seat-a\n%s one worker:demo seat-b\n%s one hub:lead\n", repo, sibling, lead)), 0600)
	for id, dir := range map[string]string{"sibling-session": sibling, "lead-session": lead} {
		_ = fleet.WriteJSON(fleet.Path("sessions", id+".json"), fleet.Rec{"session": id, "cwd": dir, "last_event_at": fleet.Now()})
	}
	if err := CmdSend("seat-b", "to-b", "question", "For B", "", "body", sid); err != nil {
		t.Fatal(err)
	}
	if err := CmdAck("to-b", sid); err == nil {
		t.Fatal("seat A acked seat B")
	}
	if err := CmdAck("to-b", "sibling-session"); err == nil {
		t.Fatal("seat A borrowed sibling session")
	}
	// Explicit named reads are allowed inside the tenant; default reads stay own.
	var out strings.Builder
	Out = &out
	if err := CmdMail("", sid, false, true); err != nil || strings.Contains(out.String(), "to-b") {
		t.Fatal(out.String(), err)
	}
	out.Reset()
	if err := CmdMail("seat-b", sid, false, true); err != nil || !strings.Contains(out.String(), "to-b") {
		t.Fatal(out.String(), err)
	}
	if err := CmdSend("worker:demo", "ambiguous", "question", "Hi", "", "body", sid); err == nil || !strings.Contains(err.Error(), "seat-a, seat-b") {
		t.Fatal(err)
	}
	// Moving the session's current cwd must not change its launch identity.
	r := fleet.SessionRecord(sid)
	r["launch_dir"], r["cwd"] = repo, sibling
	_ = fleet.WriteJSON(fleet.Path("sessions", sid+".json"), r)
	if err := os.Chdir(sibling); err != nil {
		t.Fatal(err)
	}
	if err := CmdAck("to-b", sid); err == nil {
		t.Fatal("cd let seat A ack seat B")
	}
	// A replacement in B's directory owns B's pending mailbox.
	_ = os.Remove(fleet.Path("sessions", "sibling-session.json"))
	_ = fleet.WriteJSON(fleet.Path("sessions", "replacement-b.json"), fleet.Rec{"session": "replacement-b", "cwd": sibling, "last_event_at": fleet.Now()})
	if err := CmdAck("to-b", "replacement-b"); err != nil {
		t.Fatal(err)
	}
	got, err := fleet.ReadMailFor("one", "seat-b", "to-b")
	if err != nil || fleet.S(got, "acked_by") != "replacement-b" || fleet.S(got, "from_address") != "seat-a" || fleet.S(got, "from_role") != "worker:demo" {
		t.Fatal(got, err)
	}
}

func TestMailSubjectUsageLimit(t *testing.T) {
	var refusal *Refusal
	err := CmdSend("hub:b", "large", "report", strings.Repeat("a", fleet.MaxMailSubjectBytes+1), "", "body", "")
	if !errors.As(err, &refusal) || refusal.Code != 2 {
		t.Fatal("subject limit is not usage", err)
	}
}
