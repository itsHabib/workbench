package gym

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func originWith(t *testing.T, withRef bool) string {
	t.Helper()
	root := t.TempDir()
	origin, work := filepath.Join(root, "origin.git"), filepath.Join(root, "work")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(hgit("", "init", "-q", "--bare", "-b", "main", origin))
	must(hgit("", "clone", "-q", origin, work))
	must(os.WriteFile(filepath.Join(work, "go.mod"), []byte("module kvlab\n\ngo 1.22\n"), 0o644))
	if withRef {
		ref := "testdata/kvlab/ref"
		must(fs.WalkDir(goals, ref, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, _ := goals.ReadFile(p)
			dst := filepath.Join(work, strings.TrimPrefix(p, ref+"/"))
			_ = os.MkdirAll(filepath.Dir(dst), 0o755)
			return os.WriteFile(dst, data, 0o644)
		}))
	}
	must(hgit(work, "checkout", "-q", "-b", "main"))
	must(hgit(work, "add", "-A"))
	must(hgit(work, "commit", "-q", "-m", "x"))
	must(hgit(work, "push", "-q", "origin", "main"))
	return origin
}

// The grader has to be able to say both things: everything on the reference,
// nothing on an empty module. Otherwise a score from it means nothing.
func TestTeamGraderPassesReferenceAndFailsEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test in a child module")
	}
	g := Grade("kvlab", originWith(t, true), filepath.Join(t.TempDir(), "g"))
	if g.Passed != 20 || !g.Builds || len(g.Failed) != 0 {
		t.Fatalf("reference: %+v", g)
	}
	g = Grade("kvlab", originWith(t, false), filepath.Join(t.TempDir(), "g"))
	if g.Passed != 0 || len(g.Failed) != 20 {
		t.Fatalf("empty: %+v", g)
	}
}
