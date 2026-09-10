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
	_ = fleet.WriteJSON(fleet.Path("contacts.json"), map[string][]string{"hub:a": {"hub:b"}})
	args := []string{"send", "hub:b", "--id", "m1", "--kind", "question", "--subject", "--literal", "--body", "hello", "--session", sid[:8]}
	if err := Dispatch(args); err != nil {
		t.Fatal(err)
	}
	path := fleet.MailPath("hub:b", "m1")
	before, _ := os.ReadFile(path)
	if err := Dispatch(args); err != nil {
		t.Fatal(err)
	}
	_ = fleet.WriteJSON(fleet.Path("contacts.json"), map[string][]string{})
	if err := Dispatch(args); err != nil {
		t.Fatal("retained retry depends on current contacts", err)
	}
	_ = fleet.WriteJSON(fleet.Path("contacts.json"), map[string][]string{"hub:a": {"hub:b"}})
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("retry rewrote mail")
	}
	if err := CmdAck("m1", sid); err == nil {
		t.Fatal("sender acked recipient's mail")
	}
	if err := CmdSend("hub:stranger", "m2", "question", "hi", "", "hello", sid); err == nil || !strings.Contains(err.Error(), "allowed: [hub:b]") {
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
	if _, err := os.Stat(fleet.Path("keylocks")); !os.IsNotExist(err) {
		t.Fatal("mail initialized legacy migration", err)
	}
}
