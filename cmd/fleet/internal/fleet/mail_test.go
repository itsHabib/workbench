package fleet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mailFixture(t *testing.T) string {
	t.Helper()
	oldState, oldOrg := State, OrgState
	root := t.TempDir()
	State, OrgState = filepath.Join(root, "fleet"), filepath.Join(root, "org")
	t.Cleanup(func() { State, OrgState = oldState, oldOrg })
	return root
}

func charter(t *testing.T, tenant, role string, lines ...string) {
	t.Helper()
	p := filepath.Join(OrgState, tenant, strings.ReplaceAll(role, ":", "--"), "chain.jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func charterLine(role, kind string, sup ...string) string {
	quoted := make([]string, len(sup))
	for i, s := range sup {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf(`{"v":1,"role":%q,"kind":%q,"terms":{"supervisors":[%s]}}`, role, kind, strings.Join(quoted, ","))
}

func TestMailLinesOrderedCappedAndUnackedOnly(t *testing.T) {
	mailFixture(t)
	for i := 7; i >= 1; i-- {
		rec := Rec{"id": fmt.Sprintf("m%d", i), "to": "lead:a", "from_role": "lead:top", "kind": "order", "subject": fmt.Sprintf("do %d", i), "body": "x", "at": float64(1000 + i)}
		if i == 2 {
			rec["acked_by"], rec["acked_at"] = "someone", float64(2000)
		}
		if err := WriteJSON(MailFile("lead:a", S(rec, "id")), rec); err != nil {
			t.Fatal(err)
		}
	}
	lines := MailLines("lead:a")
	want := []string{
		"[fleet] mail m1 from lead:top (order): do 1",
		"[fleet] mail m3 from lead:top (order): do 3",
		"[fleet] mail m4 from lead:top (order): do 4",
		"[fleet] mail m5 from lead:top (order): do 5",
		"[fleet] mail m6 from lead:top (order): do 6",
		"[fleet] and 1 more; fleet mail",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
	if MailLines("") != nil || MailLines("nobody:here") != nil {
		t.Fatal("lines for no role")
	}
	if roles := MailRoles(); len(roles) != 1 || roles[0] != "lead:a" {
		t.Fatal(roles)
	}
}

func TestContactsFromChartersAndDeclared(t *testing.T) {
	mailFixture(t)
	charter(t, "t", "lead:top", charterLine("lead:top", "charter", "human:mh"))
	charter(t, "t", "lead:a", charterLine("lead:a", "charter", "human:mh", "lead:top"))
	charter(t, "t", "lead:b", charterLine("lead:b", "charter", "human:mh", "lead:top"))
	charter(t, "t", "lead:far", charterLine("lead:far", "charter", "human:mh"))
	if err := WriteJSON(ContactsFile(), Rec{"lead:b": []string{"steward:x"}}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"lead:a":    "lead:b lead:top",           // parent + sibling
		"lead:top":  "lead:a lead:b",             // children
		"lead:b":    "lead:a lead:top steward:x", // + declared
		"steward:x": "lead:b",                    // declared, read symmetrically
		"lead:far":  "",                          // chartered, related to nobody
	}
	for role, want := range cases {
		if got := strings.Join(Contacts("t", role), " "); got != want {
			t.Errorf("%s: got %q want %q", role, got, want)
		}
	}
	// A recharter replaces the parent; the old sibling relation goes with it.
	charter(t, "t", "lead:b", charterLine("lead:b", "charter", "human:mh", "lead:top"), charterLine("lead:b", "recharter", "lead:far"))
	if got := strings.Join(Contacts("t", "lead:a"), " "); got != "lead:top" {
		t.Fatalf("after recharter: %q", got)
	}
	if got := strings.Join(Contacts("t", "lead:far"), " "); got != "lead:b" {
		t.Fatalf("new parent: %q", got)
	}
	if got := Contacts("other", "lead:a"); len(got) != 0 {
		t.Fatalf("another tenant: %v", got)
	}
}

func TestHookInjectsMailAtStartAndPrompt(t *testing.T) {
	root := mailFixture(t)
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git", "refs", "heads"), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/task\n"), 0o600)
	if err := os.MkdirAll(OrgState, 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(RolesMap(), []byte(repo+" t lead:a\n"), 0o644)
	if err := WriteJSON(MailFile("lead:a", "m1"), Rec{"id": "m1", "to": "lead:a", "from_role": "lead:top", "kind": "order", "subject": "ship it", "body": "x", "at": Now()}); err != nil {
		t.Fatal(err)
	}
	line := "[fleet] mail m1 from lead:top (order): ship it"
	v := Run(Event{"hook_event_name": "SessionStart", "session_id": "s1", "cwd": repo, "source": "startup"})
	if v.Code != 0 || !strings.Contains(v.Out, line) {
		t.Fatalf("SessionStart: %+v", v)
	}
	v = Run(Event{"hook_event_name": "UserPromptSubmit", "session_id": "s1", "cwd": repo, "prompt": "hi"})
	if v.Code != 0 || !strings.Contains(v.Out, line) {
		t.Fatalf("UserPromptSubmit: %+v", v)
	}
	// Reading is not acking: the record is untouched.
	if m := ReadJSON(MailFile("lead:a", "m1")); S(m, "acked_by") != "" {
		t.Fatal("hook acked the mail")
	}
	// An unroled session reads nothing.
	v = Run(Event{"hook_event_name": "SessionStart", "session_id": "s2", "cwd": root, "source": "startup"})
	if strings.Contains(v.Out, "[fleet] mail") {
		t.Fatalf("unroled session saw mail: %s", v.Out)
	}
}
