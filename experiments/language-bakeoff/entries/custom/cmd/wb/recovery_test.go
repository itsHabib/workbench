//go:build unix

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/foundation"
)

// A crash mid-apply: wb and its child are SIGKILLed while work runs, so wb
// records nothing about the outcome. A fresh caller must learn what happened
// from backend facts alone and finish the job without redoing settled work.
func TestInterruptedApplyIsVisibleToAFreshCaller(t *testing.T) {
	f := newFixture(t)
	started, hold := filepath.Join(f.root, "started"), filepath.Join(f.root, "hold")
	f.source(fmt.Sprintf(`keep file notes {
  path "notes.txt"
  text "slow input\n"
}

run command slow {
  stdin  notes.path
  argv   "sh" "-c" "touch '%s'; while [ -e '%s' ]; do sleep 0.05; done; cat"
  stdout "slow.txt"
}
`, started, hold))
	f.must(os.WriteFile(hold, nil, 0o644))

	cmd := exec.Command(wbBin, "apply", "-dir", "ws", "pipeline.wb")
	cmd.Dir = f.root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	f.must(cmd.Start())
	waitFor(t, started)
	f.must(syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)) // wb and the child, no cleanup
	_ = cmd.Wait()

	equal(t, "journal after the kill", f.ops(), []string{"converge notes", "start slow"})
	equal(t, "slow.txt after the kill", f.read("slow.txt"), "<absent>")

	r := f.plan()
	r.want(t, 0)
	contains(t, r.stdout,
		"= keep file    notes  notes.txt matches",
		"> run  command slow   run, writes slow.txt",
		"* attempt #2 was interrupted: it started but never recorded an outcome")

	f.must(os.Remove(hold))
	f.mustApply()
	equal(t, "slow.txt", f.read("slow.txt"), "slow input\n")
	equal(t, "partial output left behind", f.read("slow.txt.wb-partial"), "<absent>")
	equal(t, "journal", f.ops(), []string{"converge notes", "start slow", "start slow", "finish slow"})
}

// A journal line torn by a crash is ignored with a note, and the next
// record is sealed onto its own line so the journal stays readable.
func TestTornJournalLineIsIgnoredAndSealed(t *testing.T) {
	f := newFixture(t)
	f.pipeline("the quick brown fox\n")
	f.mustApply()
	journal := filepath.Join(f.ws, foundation.JournalPath)
	j, err := os.OpenFile(journal, os.O_APPEND|os.O_WRONLY, 0)
	f.must(err)
	_, err = j.WriteString(`{"seq":6,"op":"start","id":"sh`)
	f.must(err)
	f.must(j.Close())

	f.pipeline("the quick red fox\n")
	r := f.plan()
	contains(t, r.stdout, "note: the journal has 1 unreadable line(s), ignored")
	f.mustApply()
	r = f.wb("evidence", "-dir", "ws")
	r.want(t, 0)
	contains(t, r.stdout, "(1 unreadable line(s) ignored)", "#6   converge notes")
	f.plan().want(t, 0)
	contains(t, f.plan().stdout, "plan: no changes: 3 unchanged")
}

func waitFor(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
