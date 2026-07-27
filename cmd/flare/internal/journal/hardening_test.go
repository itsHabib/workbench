package journal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/source"
)

func TestLoadCursorsCorruptReturnsSentinelAndEmptyState(t *testing.T) {
	// The wedge bug: a corrupt cursors.json made every cycle abort at load, so
	// flare stopped delivering silently. Load must instead flag it recoverable
	// (ErrCursorsCorrupt) and hand back an empty, usable state to resweep — and
	// must NOT mutate the bad file (quarantine is the caller's call).
	dir := t.TempDir()
	j, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(j.cursorsPath(), []byte("{\"last_poll\":\"x\"}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	cur, err := j.LoadCursors()
	if !errors.Is(err, ErrCursorsCorrupt) {
		t.Fatalf("corrupt cursors must return ErrCursorsCorrupt, got %v", err)
	}
	if cur.Sources == nil {
		t.Fatal("recovered cursors must be a usable empty map, not nil")
	}
	if len(cur.Sources) != 0 || !cur.LastPoll.IsZero() {
		t.Fatalf("recovered cursors must be empty, got %+v", cur)
	}
	if _, statErr := os.Stat(j.cursorsPath()); statErr != nil {
		t.Fatal("LoadCursors must not delete/move the corrupt file — that is quarantine's job")
	}
}

func TestQuarantineCursorsMovesAsideAndClears(t *testing.T) {
	dir := t.TempDir()
	j, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(j.cursorsPath(), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	bak, err := j.QuarantineCursors()
	if err != nil {
		t.Fatal(err)
	}
	if bak == "" {
		t.Fatal("quarantining an existing file must return its backup path")
	}
	if _, statErr := os.Stat(j.cursorsPath()); !os.IsNotExist(statErr) {
		t.Fatal("quarantine must leave no cursors.json so the next save starts clean")
	}
	if _, statErr := os.Stat(bak); statErr != nil {
		t.Fatalf("the quarantined copy must exist at %s", bak)
	}
}

func TestQuarantineCursorsMissingIsNoop(t *testing.T) {
	j, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bak, err := j.QuarantineCursors()
	if err != nil {
		t.Fatalf("quarantining a missing file must be a no-op, got %v", err)
	}
	if bak != "" {
		t.Fatalf("a missing file has no backup path, got %q", bak)
	}
}

func TestSaveCursorsLeavesNoFixedSharedTemp(t *testing.T) {
	// Regression guard for the root cause: a fixed cursors.json.tmp shared by
	// every writer let two processes interleave into it and rename corrupt JSON.
	// The temp must be unique per write and cleaned up — never the fixed name.
	dir := t.TempDir()
	j, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := Cursors{LastPoll: time.Now(), Sources: map[string]source.Cursor{"gate": {Offset: 7}}}
	if err := j.SaveCursors(c); err != nil {
		t.Fatal(err)
	}
	if _, statErr := os.Stat(j.cursorsPath() + ".tmp"); !os.IsNotExist(statErr) {
		t.Fatal("SaveCursors must not use (or leave) the fixed cursors.json.tmp")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("a temp file leaked after save: %s", e.Name())
		}
	}
}

func TestLockWatchExcludesSecondInstance(t *testing.T) {
	// Two concurrent watchers (or a sweep racing a watcher) are what corrupted
	// the state. The second acquirer must be refused with ErrWatchLocked, and
	// the lock must be reclaimable once the first releases.
	j, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	release, err := j.LockWatch()
	if err != nil {
		t.Fatalf("first LockWatch must succeed, got %v", err)
	}
	if _, err := j.LockWatch(); !errors.Is(err, ErrWatchLocked) {
		t.Fatalf("second LockWatch must return ErrWatchLocked, got %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	release2, err := j.LockWatch()
	if err != nil {
		t.Fatalf("LockWatch must succeed once released, got %v", err)
	}
	if err := release2(); err != nil {
		t.Fatal(err)
	}
}
