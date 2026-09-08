package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPostWriteEvidenceOnlyObservedTarget(t *testing.T) {
	oldState, oldOrg := State, OrgState
	root := t.TempDir()
	State, OrgState = filepath.Join(root, "fleet"), filepath.Join(root, "org")
	t.Cleanup(func() { State, OrgState = oldState, oldOrg })
	repo := filepath.Join(root, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git", "refs", "heads"), 0700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/task\n"), 0600)
	ev := Event{"session_id": "worker", "cwd": repo, "hook_event_name": "PostToolUse", "tool_name": "Read", "tool_input": Rec{"file_path": filepath.Join(repo, "file")}}
	if len(postWriteEvidence(ev, "worker")) != 0 {
		t.Fatal("read counted as write activity")
	}
	ev["tool_name"] = "Edit"
	got := postWriteEvidence(ev, "worker")
	if S(M(got, "last_write"), "key") != Scope(repo, "task") || F(M(got, "last_write"), "at") == 0 {
		t.Fatal(got)
	}
	// Actual hook persists this fact for both adapter faces; it is not an agent-written acceptance.
	if v := Run(ev); v.Code != 0 {
		t.Fatal(v)
	}
	if S(M(SessionRecord("worker"), "last_write"), "key") != Scope(repo, "task") {
		t.Fatal(SessionRecord("worker"))
	}
	ev["tool_input"] = Rec{"file_path": filepath.Join(root, "outside")}
	if len(postWriteEvidence(ev, "worker")) != 0 {
		t.Fatal("outside target attributed to task")
	}
}
