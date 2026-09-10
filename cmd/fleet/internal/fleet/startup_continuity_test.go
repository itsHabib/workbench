package fleet

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func continuityFixture(t *testing.T) string {
	t.Helper()
	oldState, oldOrg := State, OrgState
	State, OrgState = t.TempDir(), t.TempDir()
	t.Cleanup(func() { State, OrgState = oldState, oldOrg })
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/task\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(RolesMap(), []byte(root+" one lead:demo seat-a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestStartupAssignmentSurvivesReplacementButNotSeatReuse(t *testing.T) {
	root := continuityFixture(t)
	p := Path("assign", "seat-a.json")
	a := Rec{"slot": "seat-a", "path": root, "repo": RepoID(root), "branch": "task", "brief": "finish this work", "at": Now(), "role": "lead:demo", "tenant": "one"}
	if err := WriteJSON(p, a); err != nil {
		t.Fatal(err)
	}
	start := func(sid string) string {
		t.Helper()
		v := Run(Event{"session_id": sid, "cwd": root, "hook_event_name": "SessionStart"})
		if v.Code != 0 {
			t.Fatal(v)
		}
		return v.Out
	}
	if !strings.Contains(start("original"), "finish this work") {
		t.Fatal("original session did not see assignment")
	}
	before, _ := os.ReadFile(p)
	if !strings.Contains(start("replacement"), "finish this work") {
		t.Fatal("replacement session did not see current assignment")
	}
	after, _ := os.ReadFile(p)
	if !bytes.Equal(before, after) || S(ReadJSON(p), "delivered_to") != "original" {
		t.Fatal("replacement changed historical delivery stamp")
	}
	for _, key := range []string{"repo", "branch", "path", "slot"} {
		saved := a[key]
		a[key] = "different"
		if err := WriteJSON(p, a); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(start("reused"), "finish this work") {
			t.Fatalf("replayed stale %s assignment", key)
		}
		a[key] = saved
	}
}

func TestStartupAssignmentRefusesReboundAndLegacyIdentity(t *testing.T) {
	root := continuityFixture(t)
	path := Path("assign", "seat-a.json")
	a := Rec{"slot": "seat-a", "path": root, "repo": RepoID(root), "branch": "task", "brief": "private old brief", "at": Now(), "role": "lead:demo", "tenant": "one"}
	for _, binding := range []string{"two lead:demo", "one lead:other"} {
		if err := WriteJSON(path, a); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(RolesMap(), []byte(root+" "+binding+" seat-a\n"), 0600); err != nil {
			t.Fatal(err)
		}
		assertStartupAssignmentHidden(t, root, path, "different role or tenant")
	}
	if err := os.WriteFile(RolesMap(), []byte(root+" one lead:demo seat-a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	delete(a, "role")
	delete(a, "tenant")
	if err := WriteJSON(path, a); err != nil {
		t.Fatal(err)
	}
	assertStartupAssignmentHidden(t, root, path, "identity is unknown")
}

func assertStartupAssignmentHidden(t *testing.T, root, path, notice string) {
	t.Helper()
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	v := Run(Event{"session_id": "replacement", "cwd": root, "hook_event_name": "SessionStart"})
	if strings.Contains(v.Out, "private old brief") || !strings.Contains(v.Out, notice) {
		t.Fatal("assignment content replayed or recovery guidance missing", v.Out)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("rejected assignment changed", err)
	}
}

func TestRoleHandoffLaunchIdentityAndBoundedStartup(t *testing.T) {
	root := continuityFixture(t)
	if err := os.WriteFile(RolesMap(), []byte(root+" one lead:demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rec := Rec{"session": "author", "launch_dir": root, "cwd": t.TempDir()}
	body := strings.Repeat("界", 2000)
	if err := WriteRoleHandoff(rec, body, "answer the worker"); err != nil {
		t.Fatal(err)
	}
	line := RoleHandoffLine(rec)
	if len(line) > roleHandoffContextBytes || !utf8.ValidString(line) || !strings.Contains(line, "authored role handoff") || !strings.Contains(line, "advisory") {
		t.Fatalf("invalid handoff excerpt (%d bytes): %s", len(line), line)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte("ref: refs/heads/other\n"), 0600); err != nil {
		t.Fatal(err)
	}
	v := Run(Event{"session_id": "replacement", "cwd": root, "hook_event_name": "SessionStart"})
	if v.Code != 0 || !strings.Contains(v.Out, "authored role handoff") {
		t.Fatal("new branch did not inherit role handoff", v)
	}
	for _, identity := range []string{"two lead:demo", "one lead:other"} {
		if err := os.WriteFile(RolesMap(), []byte(root+" "+identity+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if got := RoleHandoffLine(rec); got != "" {
			t.Fatalf("handoff leaked to %s: %s", identity, got)
		}
	}
}

func TestRoleHandoffRejectsUnknownIdentityAndOversize(t *testing.T) {
	root := continuityFixture(t)
	if err := os.WriteFile(RolesMap(), []byte(root+" one lead:demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rec := Rec{"session": "author", "launch_dir": root}
	for _, body := range []string{" ", strings.Repeat("x", roleHandoffBodyBytes+1), string([]byte{0xff})} {
		if err := WriteRoleHandoff(rec, body, ""); err == nil {
			t.Fatal("accepted invalid body")
		}
	}
	if err := WriteRoleHandoff(Rec{"session": "unknown", "launch_dir": t.TempDir(), "cwd": root}, "wrong identity", ""); err == nil {
		t.Fatal("cwd borrowed a launch identity")
	}
	if err := WriteJSON(roleHandoffPath("one", "lead:demo"), Rec{"tenant": "two", "role": "lead:demo", "conclusion": "wrong tenant"}); err != nil {
		t.Fatal(err)
	}
	if got := RoleHandoffLine(rec); got != "" {
		t.Fatal("read mismatched identity", got)
	}
}

func TestRoleHandoffDoesNotSharePooledSeatContext(t *testing.T) {
	root := continuityFixture(t)
	rec := Rec{"session": "worker", "launch_dir": root}
	if err := WriteRoleHandoff(rec, "worker-specific context", ""); err == nil || !strings.Contains(err.Error(), "branch handoffs") {
		t.Fatal("seat caller did not receive branch handoff guidance", err)
	}
	if err := WriteJSON(roleHandoffPath("one", "lead:demo"), Rec{"tenant": "one", "role": "lead:demo", "conclusion": "another seat's context"}); err != nil {
		t.Fatal(err)
	}
	if got := RoleHandoffLine(rec); got != "" {
		t.Fatal("pooled seat inherited shared role context", got)
	}
}
