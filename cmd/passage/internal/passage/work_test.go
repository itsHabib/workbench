package passage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fixture struct{ work, root, proof string }

func setup(t *testing.T) fixture {
	t.Helper()
	f := fixture{root: t.TempDir(), work: filepath.Join(t.TempDir(), "work.json"), proof: filepath.Join(t.TempDir(), "test.txt")}
	write(t, filepath.Join(f.root, "design.md"), "first design")
	write(t, f.proof, "observed the acceptance case")
	c := Contract{Name: "demo", Phases: []Phase{
		{Name: "design", Brief: "Decide", Inputs: []string{"design.md"}, Requires: []string{"accepted"}},
		{Name: "implementation", Brief: "Implement", Inputs: []string{"design.md"}, Requires: []string{"tests"}},
	}}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(t.TempDir(), "contract.json")
	write(t, config, string(b))
	if err := Init(f.work, f.root, config); err != nil {
		t.Fatal(err)
	}
	return f
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func change(t *testing.T, f fixture, action string) Change {
	t.Helper()
	r, err := Read(f.work)
	if err != nil {
		t.Fatal(err)
	}
	return Change{Action: action, Expect: len(r.Events), By: "test-reader", Note: "checked acceptance example", Requirement: "accepted", Verdict: "pass", Evidence: f.proof}
}

func update(t *testing.T, f fixture, c Change) {
	t.Helper()
	if err := Update(f.work, c); err != nil {
		t.Fatal(err)
	}
}

func status(t *testing.T, f fixture) Status {
	t.Helper()
	r, err := Read(f.work)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Inspect(r)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHandoffFailureRepairAndReopen(t *testing.T) {
	f := setup(t)
	if err := Update(f.work, change(t, f, "advance")); err == nil {
		t.Fatal("admitted missing evidence")
	}
	c := change(t, f, "record")
	c.Verdict = "fail"
	update(t, f, c)
	if status(t, f).Ready {
		t.Fatal("failure was ready")
	}
	update(t, f, change(t, f, "record"))
	if !status(t, f).Ready {
		t.Fatal("passing evidence not ready")
	}
	update(t, f, change(t, f, "advance"))
	write(t, filepath.Join(f.root, "design.md"), "changed decision")
	s := status(t, f)
	if !strings.Contains(strings.Join(s.Problems, " "), "reopen design") {
		t.Fatalf("missing upstream drift: %+v", s)
	}
	c = change(t, f, "record")
	c.Requirement = "tests"
	if err := Update(f.work, c); err == nil {
		t.Fatal("accepted downstream evidence after upstream change")
	}
	c = change(t, f, "reopen")
	c.Phase = "design"
	update(t, f, c)
	if status(t, f).Ready {
		t.Fatal("reopen reused old evidence")
	}
	update(t, f, change(t, f, "record"))
	update(t, f, change(t, f, "advance"))
	c = change(t, f, "record")
	c.Requirement = "tests"
	update(t, f, c)
	update(t, f, change(t, f, "advance"))
	if s := status(t, f); s.Phase != "complete" || !s.Ready {
		t.Fatalf("not complete: %+v", s)
	}
	r, err := Read(f.work)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Events) != 8 || r.Events[0].Receipt.Verdict != "fail" {
		t.Fatal("history was lost")
	}
}

func TestEvidenceIsRetainedButChangedInputsAreStale(t *testing.T) {
	f := setup(t)
	update(t, f, change(t, f, "record"))
	write(t, f.proof, "overwritten evidence")
	if !status(t, f).Ready {
		t.Fatal("external evidence overwrite changed retained proof")
	}
	r, err := Read(f.work)
	if err != nil {
		t.Fatal(err)
	}
	if r.Events[0].Receipt.Content != "observed the acceptance case" {
		t.Fatal("evidence not retained")
	}
	write(t, filepath.Join(f.root, "design.md"), "new design")
	if status(t, f).Ready {
		t.Fatal("stale evidence ready")
	}
	if err := Update(f.work, change(t, f, "advance")); err == nil {
		t.Fatal("advanced stale evidence")
	}
}

func TestConcurrentAndLostReplyDoNotRepeatMutation(t *testing.T) {
	f := setup(t)
	c := change(t, f, "record")
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { results <- Update(f.work, c) })
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("accepted %d writers", success)
	}
	if err := Update(f.work, c); err == nil {
		t.Fatal("lost-response retry repeated mutation")
	}
	if status(t, f).Revision != 1 {
		t.Fatal("unexpected revision")
	}
}

func TestDamagedHistoryAndInterruptedLockRefuseWithoutWriting(t *testing.T) {
	f := setup(t)
	update(t, f, change(t, f, "record"))
	original, err := os.ReadFile(f.work)
	if err != nil {
		t.Fatal(err)
	}
	c := change(t, f, "advance")
	write(t, f.work+".lock", "")
	if err := Update(f.work, c); err == nil {
		t.Fatal("ignored interrupted lock")
	}
	after, err := os.ReadFile(f.work)
	if err != nil {
		t.Fatal(err)
	}
	if string(original) != string(after) {
		t.Fatal("changed locked record")
	}
	if err := os.Remove(f.work + ".lock"); err != nil {
		t.Fatal(err)
	}
	write(t, f.work, string(original[:len(original)/2]))
	if _, err := Read(f.work); err == nil {
		t.Fatal("read truncated history")
	}
	write(t, f.work, strings.Replace(string(original), "observed the acceptance case", "fabricated replacement", 1))
	if _, err := Read(f.work); err == nil {
		t.Fatal("accepted corrupted retained evidence")
	}
}

func TestGitRevisionAndDirtyTree(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.name", "Test"}, {"config", "user.email", "test@example.invalid"}, {"commit", "--allow-empty", "-m", "first"}} {
		if _, err := git(root, args...); err != nil {
			t.Fatal(err)
		}
	}
	p := Phase{Git: true}
	before, err := snapshot(root, p)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "untracked"), "dirty")
	if _, err := snapshot(root, p); err == nil {
		t.Fatal("accepted dirty tree")
	}
	if _, err := git(root, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := git(root, "commit", "-m", "second"); err != nil {
		t.Fatal(err)
	}
	after, err := snapshot(root, p)
	if err != nil {
		t.Fatal(err)
	}
	if before.Head == after.Head || before.Head == "" {
		t.Fatal("head binding missing")
	}
}

func TestUnknownRequirementAndForwardReopenRefuse(t *testing.T) {
	f := setup(t)
	c := change(t, f, "record")
	c.Requirement = "invented"
	if err := Update(f.work, c); err == nil {
		t.Fatal("unknown requirement accepted")
	}
	c = change(t, f, "reopen")
	c.Phase = "implementation"
	if err := Update(f.work, c); err == nil {
		t.Fatal("skipped ahead")
	}
	if status(t, f).Revision != 0 {
		t.Fatal("refusal changed history")
	}
}
