package fleet

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func mailFixture(t *testing.T) string {
	t.Helper()
	oldState, oldOrg := State, OrgState
	State, OrgState = t.TempDir(), t.TempDir()
	t.Cleanup(func() { State, OrgState = oldState, oldOrg })
	root := t.TempDir()
	text := ""
	for _, role := range []string{"hub:root", "hub:a", "hub:b", "hub:child", "hub:stranger"} {
		dir := filepath.Join(root, Safe(role))
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		text += fmt.Sprintf("%s one %s\n", dir, role)
	}
	if err := os.WriteFile(RolesMap(), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func mailPayload(id string) Rec {
	return Rec{"id": id, "to": "hub:a", "from_role": "hub:root", "from_session": "sender", "kind": "question", "subject": "A question", "head": "abc123", "body": "Please answer"}
}
func mustPut(t *testing.T, r Rec) Rec {
	t.Helper()
	out, err := PutMail(r)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMailReplayAckAndConflict(t *testing.T) {
	mailFixture(t)
	r := mustPut(t, mailPayload("same"))
	if F(r, "at") == 0 {
		t.Fatal(r)
	}
	ack, err := AckMail("hub:a", "same", "reader")
	if err != nil || S(ack, "acked_by") != "reader" {
		t.Fatal(ack, err)
	}
	before, _ := os.ReadFile(MailPath("hub:a", "same"))
	retry := mailPayload("same")
	retry["from_session"] = "replacement"
	mustPut(t, retry)
	_, err = AckMail("hub:a", "same", "other")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(MailPath("hub:a", "same"))
	if !bytes.Equal(before, after) {
		t.Fatal("replay changed record")
	}
	for _, k := range []string{"from_role", "kind", "subject", "head", "body"} {
		p := mailPayload("same")
		p[k] = "changed"
		if _, err := PutMail(p); err == nil {
			t.Fatalf("accepted changed %s", k)
		}
	}
	rows, err := Mail("hub:a", true)
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	p := mailPayload("same")
	p["to"] = "hub:b"
	mustPut(t, p)
}

func TestMailConcurrentWriters(t *testing.T) {
	mailFixture(t)
	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := PutMail(mailPayload("same")); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := Mail("hub:a", false)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
}

func TestMailDamagedAndUnsafePaths(t *testing.T) {
	mailFixture(t)
	for _, id := range []string{"../escape", "..", ".", "a/b", "/abs", ""} {
		if _, err := PutMail(mailPayload(id)); err == nil {
			t.Fatalf("accepted %q", id)
		}
	}
	mustPut(t, mailPayload("bad"))
	if err := os.WriteFile(MailPath("hub:a", "bad"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := PutMail(mailPayload("bad")); err == nil {
		t.Fatal("overwrote damage")
	}
	if _, err := Mail("hub:a", false); err == nil {
		t.Fatal("damage read as empty")
	}
	p := mailPayload("alias")
	p["to"] = "hub__a"
	if _, err := PutMail(p); err == nil {
		t.Fatal("aliased role accepted for write")
	}
	p["at"] = Now()
	_ = WriteJSON(MailPath("hub__a", "alias"), p)
	if _, err := ReadMail("hub:a", "alias"); err == nil {
		t.Fatal("aliased role accepted")
	}
}

func TestMailHookLinesAndNoAutoAck(t *testing.T) {
	root := mailFixture(t)
	for i := 0; i < 7; i++ {
		p := mailPayload(fmt.Sprintf("m%d", i))
		p["subject"] = "line one\nline two"
		mustPut(t, p)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit"} {
		v := Run(Event{"hook_event_name": event, "session_id": "reader", "cwd": filepath.Join(root, Safe("hub:a"))})
		out := string(DumpJSON(v))
		if strings.Count(out, "[fleet] mail m") != 5 || !strings.Contains(out, "and 2 more; fleet mail") || !strings.Contains(out, "line one line two") {
			t.Fatal(out)
		}
	}
	rows, err := Mail("hub:a", true)
	if err != nil || len(rows) != 7 {
		t.Fatal("hook acked mail", rows, err)
	}
	if _, err := AckMail("hub:a", "m0", "reader"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(MailLines("hub:a"), "\n"), "mail m0 ") {
		t.Fatal("acked mail still injected")
	}
}

func TestMailPeerTenantBoundaries(t *testing.T) {
	root := mailFixture(t)
	rec := Rec{"cwd": filepath.Join(root, Safe("hub:a"))}
	for _, to := range []string{"hub:root", "hub:b", "hub:child", "hub:stranger", "hub:a"} {
		if err := MailPeer(rec, to); err != nil {
			t.Fatal(to, err)
		}
	}
	rec["launch_dir"] = rec["cwd"]
	rec["cwd"] = filepath.Join(root, "unroled")
	if err := MailPeer(rec, "hub:b"); err != nil {
		t.Fatal("cd changed identity", err)
	}
	f, err := os.OpenFile(RolesMap(), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = fmt.Fprintf(f, "%s two hub:foreign\n", filepath.Join(root, "foreign"))
	_ = f.Close()
	if err := MailPeer(rec, "hub:foreign"); err == nil {
		t.Fatal("cross-tenant message allowed")
	}
	f, _ = os.OpenFile(RolesMap(), os.O_APPEND|os.O_WRONLY, 0600)
	_, _ = fmt.Fprintf(f, "%s two hub:a\n", filepath.Join(root, "duplicate"))
	_ = f.Close()
	if _, err := MailRoleTenant("hub:a"); err == nil {
		t.Fatal("ambiguous tenant accepted")
	}
}

func TestMailTenantPinSurvivesRebinding(t *testing.T) {
	mailFixture(t)
	mustPut(t, mailPayload("m1"))
	pin, _ := os.ReadFile(Path("mail", Safe("hub:a"), mailAddressFile))
	rows, _ := os.ReadFile(RolesMap())
	_ = os.WriteFile(RolesMap(), []byte(strings.ReplaceAll(string(rows), " one hub:a", " two hub:a")), 0600)
	if _, err := ReadMail("hub:a", "m1"); err == nil {
		t.Fatal("new tenant read retained mail")
	}
	if _, err := Mail("hub:a", false); err == nil {
		t.Fatal("new tenant listed retained mail")
	}
	if _, err := AckMail("hub:a", "m1", "new-tenant"); err == nil {
		t.Fatal("new tenant acked retained mail")
	}
	if _, err := PutMail(mailPayload("m2")); err == nil {
		t.Fatal("new tenant reused pinned mailbox")
	}
	after, _ := os.ReadFile(Path("mail", Safe("hub:a"), mailAddressFile))
	if !bytes.Equal(pin, after) {
		t.Fatal("mailbox pin changed")
	}
	_ = os.Remove(Path("mail", Safe("hub:a"), mailAddressFile))
	if _, err := PutMail(mailPayload("m2")); err == nil {
		t.Fatal("retained mailbox adopted without pin")
	}
}

func TestMailPromptUsesCurrentLaunchRole(t *testing.T) {
	root := mailFixture(t)
	old := mailPayload("old")
	mustPut(t, old)
	fresh := mailPayload("fresh")
	fresh["to"] = "hub:b"
	mustPut(t, fresh)
	cwd := filepath.Join(root, Safe("hub:a"))
	ev := Event{"hook_event_name": "SessionStart", "session_id": "reader", "cwd": cwd}
	Run(ev)
	rows, _ := os.ReadFile(RolesMap())
	_ = os.WriteFile(RolesMap(), []byte(strings.ReplaceAll(string(rows), " one hub:a", " one hub:b")), 0600)
	ev["hook_event_name"] = "UserPromptSubmit"
	v := Run(ev)
	if !strings.Contains(v.Out, "mail fresh ") || strings.Contains(v.Out, "mail old ") {
		t.Fatal(v.Out)
	}
}
