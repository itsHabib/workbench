package verbs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/cmd/fleet/internal/fleet"
)

func TestHandoffTyposPreserveCheckpoints(t *testing.T) {
	repo, sid := requestFixture(t)
	if err := os.MkdirAll(fleet.OrgState, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleet.RolesMap(), []byte(repo+" one lead:demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"handoff", "task", "branch conclusion", "next step"},
		{"handoff", "--role", "role conclusion", "next step", "--session", sid},
	} {
		if err := Dispatch(args); err != nil {
			t.Fatal(err)
		}
	}
	// A malformed command must not even rekey an older checkpoint on its way
	// to reporting the typo.
	legacy := fleet.Path("handoff", "legacy.json")
	if err := fleet.WriteJSON(legacy, fleet.Rec{"repo": "old", "branch": "task", "conclusion": "legacy conclusion"}); err != nil {
		t.Fatal(err)
	}
	before := handoffFiles(t)
	for _, args := range [][]string{
		{"task", "--show"}, {"task", "--list"}, {"task", "-h"},
		{"task", "conclusion", "--show"}, {"task", "conclusion", "next", "extra"},
		{"task", "conclusion", "--session", ""}, {"task"},
		{"--role", "--show"}, {"--role", "--list"},
		{"--role", "conclusion", "--session"},
		{"--role", "conclusion", "--session", "--show"},
		{"--role", "conclusion", "--session", ""},
		{"--role", "conclusion", "--role"},
		{"--role", "conclusion", "--session", sid, "--session", sid},
		{"--role", "conclusion", "next", "extra"},
	} {
		t.Run(filepath.Join(args...), func(t *testing.T) {
			var refusal *Refusal
			err := Dispatch(append([]string{"handoff"}, args...))
			if !errors.As(err, &refusal) || refusal.Code != 2 {
				t.Fatalf("expected usage error, got %v", err)
			}
			after := handoffFiles(t)
			if len(after) != len(before) {
				t.Fatal("checkpoint files changed")
			}
			for path, content := range before {
				if !bytes.Equal(content, after[path]) {
					t.Fatalf("checkpoint changed: %s", path)
				}
			}
		})
	}
}

func handoffFiles(t *testing.T) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	for _, dir := range []string{"handoff", "role-handoff"} {
		err := filepath.WalkDir(fleet.Path(dir), func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				files[path] = readBytes(t, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func TestHandoffLiteralOptionText(t *testing.T) {
	repo, _ := requestFixture(t)
	if err := Dispatch([]string{"handoff", "task", "--", "--show is not a read command", "- preserve the draft"}); err != nil {
		t.Fatal(err)
	}
	rec := fleet.ReadJSON(fleet.KeyFile("handoff", fleet.Scope(repo, "task")))
	if fleet.S(rec, "conclusion") != "--show is not a read command" || fleet.S(rec, "next") != "- preserve the draft" {
		t.Fatal(rec)
	}
}
