package foundation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests reach the write itself, not just planning: they are the
// guarantee that an effect cannot leave the workspace or follow a link.

func setup(t *testing.T) (*Workspace, string, string) {
	t.Helper()
	root := t.TempDir()
	dir, outside := filepath.Join(root, "ws"), filepath.Join(root, "outside")
	for _, d := range []string{dir, outside} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ws.Close)
	return ws, dir, outside
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return "<absent>"
	}
	return string(b)
}

func TestWriteNeverFollowsAPlantedTemporaryLink(t *testing.T) {
	ws, dir, _ := setup(t)
	diary := filepath.Join(dir, "diary.txt")
	if err := os.WriteFile(diary, []byte("private\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("diary.txt", filepath.Join(dir, "notes.txt"+TempSuffix)); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteFile("notes.txt", []byte("desired\n")); err != nil {
		t.Fatal(err)
	}
	if got := read(t, diary); got != "private\n" {
		t.Errorf("diary.txt = %q: the write followed the planted link", got)
	}
	info, err := os.Lstat(filepath.Join(dir, "notes.txt"))
	if err != nil || !info.Mode().IsRegular() || read(t, filepath.Join(dir, "notes.txt")) != "desired\n" {
		t.Errorf("notes.txt is not a regular file holding the desired text: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "notes.txt"+TempSuffix)); !os.IsNotExist(err) {
		t.Errorf("temporary file left behind: %v", err)
	}
}

func TestWritesCannotLeaveTheWorkspace(t *testing.T) {
	ws, dir, outside := setup(t)
	if err := os.Symlink(outside, filepath.Join(dir, "sub")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "t.txt"), filepath.Join(dir, "x.txt"+TempSuffix)); err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteFile("sub/deep.txt", []byte("escaped\n")); err == nil {
		t.Error("writing through a link that leaves the workspace succeeded")
	}
	if err := ws.WriteFile("x.txt", []byte("kept inside\n")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Errorf("files appeared outside the workspace: %v %v", entries, err)
	}
}

func TestObserveTargetFollowsLinksOnlyInsideTheWorkspace(t *testing.T) {
	ws, dir, outside := setup(t)
	if err := os.Mkdir(filepath.Join(dir, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real", "data.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real/data.txt", filepath.Join(dir, "data.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "away")); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := ws.Observe("data.txt"); got != Other {
		t.Errorf("Observe(link) = %s, want %s: owned paths are described without following links", got, Other)
	}
	if got, _, _ := ws.ObserveTarget("data.txt"); got != DigestBytes([]byte("one\n")) {
		t.Errorf("ObserveTarget(link) = %s, want the digest of the linked content", got)
	}
	if _, _, err := ws.ObserveTarget("away/secret"); err == nil || !strings.Contains(err.Error(), "escapes") {
		t.Errorf("ObserveTarget through a link that leaves the workspace: err = %v", err)
	}
}

func TestNoWriteThroughASymlinkedParentEvenInside(t *testing.T) {
	ws, dir, _ := setup(t)
	if err := os.Mkdir(filepath.Join(dir, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	for name, err := range map[string]error{
		"WriteFile": ws.WriteFile("alias/x.txt", []byte("x")),
		"MkdirAll":  ws.MkdirAll("alias/sub"),
	} {
		if err == nil || !strings.Contains(err.Error(), "parent alias is a symlink") {
			t.Errorf("%s through a symlinked parent: err = %v", name, err)
		}
	}
	if _, _, err := ws.Observe("alias/x.txt"); err == nil {
		t.Error("Observe of an owned path under a symlinked parent must fail")
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "real")); len(entries) != 0 {
		t.Errorf("wrote through the link: %v", entries)
	}
}
