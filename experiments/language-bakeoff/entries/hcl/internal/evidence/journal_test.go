package evidence_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/hcl/internal/evidence"
)

// A crash mid-append leaves a partial last line. It is counted and ignored,
// and the next append starts on a fresh line so it stays readable.
func TestTornAppendIsIgnoredAndIsolated(t *testing.T) {
	dir := t.TempDir()
	good := `{"seq":1,"run":1,"address":"file.a","action":"create","event":"succeeded"}` + "\n"
	writeJournal(t, dir, good+`{"seq":2,"run":2,"addr`)

	j := open(t, dir, 1, 1)
	if err := j.Append(evidence.Entry{Run: j.NextRun(), Address: "file.a", Event: evidence.Started}); err != nil {
		t.Fatal(err)
	}

	last, _ := open(t, dir, 2, 1).Latest("file.a")
	if last.Seq != 2 || last.Run != 2 || last.Event != evidence.Started {
		t.Errorf("latest %+v", last)
	}
}

func writeJournal(t *testing.T, dir, content string) {
	t.Helper()
	path := filepath.Join(dir, evidence.File)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func open(t *testing.T, dir string, entries, unreadable int) *evidence.Journal {
	t.Helper()
	j, err := evidence.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Entries()) != entries || j.Unreadable != unreadable {
		t.Fatalf("entries %d unreadable %d, want %d and %d", len(j.Entries()), j.Unreadable, entries, unreadable)
	}
	return j
}
