package fleet

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func addMailBindings(t *testing.T, lines string) {
	t.Helper()
	f, err := os.OpenFile(RolesMap(), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(lines)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func seedLegacyMail(t *testing.T, r Rec, tenant string) string {
	t.Helper()
	dir := Path("mail", Safe(S(r, "to")))
	if err := WriteJSON(filepath.Join(dir, mailAddressFile), Rec{"role": S(r, "to"), "tenant": tenant}); err != nil {
		t.Fatal(err)
	}
	r["at"] = Now()
	path := filepath.Join(dir, S(r, "id")+".json")
	if err := WriteJSON(path, r); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSeatMailIndependentInboxesAndHooks(t *testing.T) {
	root := mailFixture(t)
	a, b := filepath.Join(root, "seat-a"), filepath.Join(root, "seat-b")
	addMailBindings(t, fmt.Sprintf("%s one worker:demo seat-a\n%s one worker:demo seat-b\n", a, b))
	for _, address := range []string{"seat-a", "seat-b"} {
		p := mailPayload("same")
		p["to"], p["body"], p["subject"] = address, address, "for "+address
		mustPut(t, p)
	}
	for _, dir := range []string{a, b} {
		address := filepath.Base(dir)
		ev := Event{"hook_event_name": "SessionStart", "session_id": address, "cwd": dir}
		got := Run(ev)
		if !strings.Contains(got.Out, "for "+address) {
			t.Fatal(got.Out)
		}
		if strings.Count(got.Out, "[fleet] mail same ") != 1 {
			t.Fatal(got.Out)
		}
		ev["session_id"] = address + "-replacement"
		got = Run(ev)
		if !strings.Contains(got.Out, "for "+address) {
			t.Fatal("replacement lost mail", got.Out)
		}
	}
	if _, err := ResolveMailbox("one", "worker:demo"); err == nil || !strings.Contains(err.Error(), "seat-a, seat-b") {
		t.Fatal(err)
	}
	if _, err := AckMailFor("one", "seat-a", "same", "seat-a-replacement"); err != nil {
		t.Fatal(err)
	}
	rows, err := MailFor("one", "seat-b", true)
	if err != nil || len(rows) != 1 {
		t.Fatal("other seat acked", rows, err)
	}
}

func TestSeatSenderIdentityCannotClaimSiblingRetry(t *testing.T) {
	mailFixture(t)
	p := mailPayload("same")
	p["from_role"], p["from_address"], p["from_kind"] = "worker:demo", "seat-a", "seat"
	first := mustPut(t, p)
	p["from_session"] = "replacement"
	retry := mustPut(t, p)
	if S(first, "from_session") != S(retry, "from_session") {
		t.Fatal(first, retry)
	}
	p["from_address"] = "seat-b"
	if _, err := PutMail(p, "one"); err == nil {
		t.Fatal("sibling claimed retry identity")
	}
	p["id"] = "other"
	mustPut(t, p)
}

func TestTypedMailAliasesAndDuplicateTenantNames(t *testing.T) {
	root := mailFixture(t)
	addMailBindings(t, fmt.Sprintf("%s one hub__a\n%s two hub:a\n%s two hub:root\n", filepath.Join(root, "alias"), filepath.Join(root, "two-a"), filepath.Join(root, "two-root")))
	paths := map[string]bool{}
	for _, pair := range [][2]string{{"one", "hub:a"}, {"one", "hub__a"}, {"two", "hub:a"}} {
		p := mailPayload("same")
		p["tenant"], p["to"], p["body"] = pair[0], pair[1], strings.Join(pair[:], "/")
		mustPut(t, p)
		path, err := MailPathFor(pair[0], pair[1], "same")
		if err != nil || paths[path] {
			t.Fatal(path, err)
		}
		paths[path] = true
		r, err := ReadMailFor(pair[0], pair[1], "same")
		if err != nil || S(r, "body") != S(p, "body") {
			t.Fatal(r, err)
		}
	}
	if _, err := AckMailFor("two", "hub:a", "same", "reader-two"); err != nil {
		t.Fatal(err)
	}
	r, err := ReadMailFor("one", "hub:a", "same")
	if err != nil || Has(r, "acked_at") {
		t.Fatal("ack crossed tenant", r, err)
	}
}

func TestLegacyRoleMailReadAckAndReplacementRetry(t *testing.T) {
	mailFixture(t)
	p := mailPayload("legacy")
	path := seedLegacyMail(t, p, "one")
	original, _ := os.ReadFile(path)
	rows, err := MailFor("one", "hub:a", true)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	afterRead, _ := os.ReadFile(path)
	if !bytes.Equal(original, afterRead) {
		t.Fatal("read migrated record")
	}
	r, err := AckMailFor("one", "hub:a", "legacy", "replacement-reader")
	if err != nil || S(r, "acked_by") != "replacement-reader" {
		t.Fatal(r, err)
	}
	acked, _ := os.ReadFile(path)
	p["from_session"] = "replacement-sender"
	r = mustPut(t, p)
	afterRetry, _ := os.ReadFile(path)
	if !bytes.Equal(acked, afterRetry) || S(r, "from_session") != "sender" {
		t.Fatal("retry changed provenance", r)
	}
	typed, _ := MailPathFor("one", "hub:a", "legacy")
	if _, err := os.Stat(typed); !os.IsNotExist(err) {
		t.Fatal("legacy duplicated", err)
	}
}

func TestLegacySharedRoleHistoryNeverBecomesSeatMail(t *testing.T) {
	root := mailFixture(t)
	p := mailPayload("old")
	p["to"] = "worker:demo"
	path := seedLegacyMail(t, p, "one")
	before, _ := os.ReadFile(path)
	addMailBindings(t, fmt.Sprintf("%s one worker:demo seat-a\n%s one worker:demo seat-b\n", filepath.Join(root, "a"), filepath.Join(root, "b")))
	rows, err := MailFor("one", "worker:demo", false)
	if err != nil || len(rows) != 1 {
		t.Fatal("history inaccessible", rows, err)
	}
	for _, seat := range []string{"seat-a", "seat-b"} {
		rows, err := MailFor("one", seat, false)
		if err != nil || len(rows) != 0 {
			t.Fatal("seat adopted history", rows, err)
		}
		if _, err := AckMailFor("one", seat, "old", "replacement"); err == nil {
			t.Fatal("seat acked history")
		}
	}
	if _, err := AckMailFor("one", "worker:demo", "old", "replacement"); err == nil {
		t.Fatal("shared role ack permitted")
	}
	if _, err := PutMail(p, "one"); err == nil {
		t.Fatal("shared role send permitted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("history altered")
	}
}

func TestLegacyPinPreventsAliasOrTenantAdoption(t *testing.T) {
	root := mailFixture(t)
	p := mailPayload("legacy")
	path := seedLegacyMail(t, p, "one")
	before, _ := os.ReadFile(path)
	addMailBindings(t, fmt.Sprintf("%s two hub:a\n%s one hub__a\n", filepath.Join(root, "two"), filepath.Join(root, "alias")))
	for _, pair := range [][2]string{{"two", "hub:a"}, {"one", "hub__a"}} {
		rows, err := MailFor(pair[0], pair[1], false)
		if err != nil || len(rows) != 0 {
			t.Fatal("pin bypassed", rows, err)
		}
		if _, err := AckMailFor(pair[0], pair[1], "legacy", "imposter"); err == nil {
			t.Fatal("pin bypassed on ack")
		}
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("legacy changed")
	}
	_ = os.Remove(filepath.Join(filepath.Dir(path), mailAddressFile))
	if _, err := MailFor("one", "hub:a", false); err == nil {
		t.Fatal("unpinned legacy read")
	}
	if _, err := PutMail(mailPayload("new"), "one"); err == nil {
		t.Fatal("unpinned legacy adopted")
	}
}

func TestMailAuthorizedTenantCannotDriftBeforePublication(t *testing.T) {
	root := mailFixture(t)
	sender, role, err := MailSender(Rec{"launch_dir": filepath.Join(root, Safe("hub:root"))})
	if err != nil {
		t.Fatal(err)
	}
	if err := MailPeer(Rec{"launch_dir": filepath.Join(root, Safe("hub:root"))}, "hub:a"); err != nil {
		t.Fatal(err)
	}
	p := mailPayload("rebind")
	p["tenant"], p["from_role"], p["from_address"], p["from_kind"] = sender.Tenant, role, sender.Address, sender.Kind
	rows, _ := os.ReadFile(RolesMap())
	_ = os.WriteFile(RolesMap(), []byte(strings.ReplaceAll(string(rows), " one hub:a", " two hub:a")), 0600)
	if _, err := PutMail(p, "one"); err == nil {
		t.Fatal("rebound target adopted authorized send")
	}
	r, err := ReadMailFor("two", "hub:a", "rebind")
	if err != nil || r != nil {
		t.Fatal("mail leaked to new tenant", r, err)
	}
	// The legacy lookup also uses the captured tenant rather than the new binding.
	legacy := mailPayload("legacy")
	path := seedLegacyMail(t, legacy, "one")
	before, _ := os.ReadFile(path)
	p["id"] = "legacy"
	if _, err := PutMail(p, "one"); err == nil {
		t.Fatal("rebound legacy target accepted")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("legacy provenance changed")
	}
}

func TestMailRefusesRoleSeatAndDuplicateSeatAmbiguity(t *testing.T) {
	root := mailFixture(t)
	addMailBindings(t, fmt.Sprintf("%s one worker:a collision\n%s one collision\n%s one worker:a duplicate\n%s one worker:a duplicate\n", filepath.Join(root, "seat"), filepath.Join(root, "role"), filepath.Join(root, "a"), filepath.Join(root, "b")))
	for _, address := range []string{"collision", "duplicate"} {
		if _, err := ResolveMailbox("one", address); err == nil {
			t.Fatal("ambiguous address accepted", address)
		}
	}
}

// The typed namespace cannot alias a legal legacy role named "v2".
func TestMailVersionNamespaceCannotAliasLegacyRole(t *testing.T) {
	root := mailFixture(t)
	addMailBindings(t, fmt.Sprintf("%s one v2\n", filepath.Join(root, "v2")))
	mustPut(t, mailPayload("normal"))
	p := mailPayload("version")
	p["to"] = "v2"
	mustPut(t, p)
	rows, err := MailFor("one", "v2", false)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
}
