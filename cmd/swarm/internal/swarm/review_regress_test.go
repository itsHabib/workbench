package swarm

// Regression tests for the defects an independent review reproduced on
// 2026-09-17 (poc/review-2026-09-17-codex). Each began as a probe that
// passed when the bad behavior occurred; each now fails if it returns.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// clock pins Now to a value the test advances.
func clock(t *testing.T) *time.Time {
	t.Helper()
	n := time.Now().UTC()
	old := Now
	Now = func() time.Time { return n }
	t.Cleanup(func() { Now = old })
	return &n
}

func refused(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), code) {
		t.Fatalf("want refusal %s, got %v", code, err)
	}
}

func TestRegressExpiredEpochCannotRule(t *testing.T) {
	s := open(t, repo(t))
	n := clock(t)
	r, _ := s.Ask("a", []string{"a.go"}, "?", nil, "")
	c, err := s.ClaimRequest(r.ID, "a", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	*n = n.Add(2 * time.Second)
	_, err = s.Rule(r.ID, "a", c.Claim.Epoch, "late", "", "", nil)
	refused(t, err, "claim_expired")
}

func TestRegressSupersededEpochCannotRule(t *testing.T) {
	s := open(t, repo(t))
	n := clock(t)
	r, _ := s.Ask("a", []string{"a.go"}, "?", nil, "")
	a, _ := s.ClaimRequest(r.ID, "a", time.Second)
	*n = n.Add(2 * time.Second)
	if _, err := s.ClaimRequest(r.ID, "b", time.Second); err != nil {
		t.Fatal(err)
	}
	*n = n.Add(2 * time.Second)
	_, err := s.Rule(r.ID, "a", a.Claim.Epoch, "stale", "", "", nil)
	refused(t, err, "claim_fenced")
	if ds, _ := s.Decisions(); len(ds) != 0 {
		t.Fatalf("a fenced ruling reached the ledger: %+v", ds)
	}
}

func TestRegressPeerCannotSupersedeOperator(t *testing.T) {
	s := open(t, repo(t))
	if err := s.Init(Tiers{Operator: []string{"op"}}); err != nil {
		t.Fatal(err)
	}
	a, err := s.Decide("op", []string{"a.go"}, "operator", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Decide("peer", []string{"a.go"}, "overridden", "", a.ID, nil)
	refused(t, err, "outranked")
	r, _ := s.Ask("peer", []string{"a.go"}, "?", nil, "")
	_, err = s.Rule(r.ID, "peer", 0, "overridden", "", a.ID, nil)
	refused(t, err, "outranked")
}

func TestRegressLiveLockSurvivesAnyAge(t *testing.T) {
	s := open(t, repo(t))
	n := clock(t)
	releaseA, err := s.lock("probe", time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	*n = n.Add(24 * time.Hour) // however old the lock looks, its holder lives
	if _, err := s.lock("probe", 100*time.Millisecond, time.Minute); err == nil {
		t.Fatal("a live holder's lock was taken by age")
	}
	releaseA()
	releaseB, err := s.lock("probe", time.Second, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	releaseA() // a late or repeated release from A must not free B's lock
	if _, err := s.lock("probe", 100*time.Millisecond, time.Minute); err == nil {
		t.Fatal("A's release removed B's lock")
	}
	releaseB()
}

func TestRegressLedgerTruncationAndTornTail(t *testing.T) {
	s := open(t, repo(t))
	if _, err := s.Decide("a", []string{"a.go"}, "one", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decide("a", []string{"b.go"}, "two", "", "", nil); err != nil {
		t.Fatal(err)
	}
	p := s.path("decisions.jsonl")
	data, _ := os.ReadFile(p)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if err := os.WriteFile(p, []byte(lines[0]+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Decisions(); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("removed tail accepted: %v", err)
	}
	// A torn append beyond the committed head is dropped, not fatal.
	if err := os.WriteFile(p, append(append([]byte{}, data...), []byte(`{"id":`)...), 0o644); err != nil {
		t.Fatal(err)
	}
	ds, err := s.Decisions()
	if err != nil || len(ds) != 2 {
		t.Fatalf("torn tail made the ledger unreadable: %v %d", err, len(ds))
	}
	// And the next append starts on its own line.
	if _, err := s.Decide("a", []string{"c.go"}, "three", "", "", nil); err != nil {
		t.Fatal(err)
	}
	if ds, err = s.Decisions(); err != nil || len(ds) != 3 {
		t.Fatalf("append after a torn tail: %v %d", err, len(ds))
	}
}

func TestRegressCrashBetweenAppendAndSaveRulesOnce(t *testing.T) {
	s := open(t, repo(t))
	r, _ := s.Ask("a", []string{"a.go"}, "?", nil, "")
	before, _ := json.Marshal(r)
	first, err := s.Rule(r.ID, "a", 0, "first", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The durable state at a crash after the ledger append and before the
	// request update: the decision exists, the request still says open.
	if err := os.WriteFile(s.path("requests", r.ID+".json"), before, 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := s.Rule(r.ID, "a", 0, "second", "", first.ID, nil)
	if err != nil || again.ID != first.ID {
		t.Fatalf("retry did not return the original ruling: %v %+v", err, again)
	}
	_, err = s.Rule(r.ID, "b", 0, "third", "", "", nil)
	refused(t, err, "already_ruled")
	if ds, _ := s.Decisions(); len(ds) != 1 {
		t.Fatalf("request ruled %d times", len(ds))
	}
}

func TestRegressAdmissionReserves(t *testing.T) {
	s := open(t, repo(t))
	a, err := s.Admit(AdmitOptions{Seats: 1, For: "x"})
	if err != nil || !a.Admitted {
		t.Fatal(a, err)
	}
	b, err := s.Admit(AdmitOptions{Seats: 1, For: "y"})
	if err != nil || b.Admitted {
		t.Fatalf("two launchers admitted onto one seat: %+v %v", b, err)
	}
	// The same launcher retrying its own admission is not refused by itself.
	if a, _ = s.Admit(AdmitOptions{Seats: 1, For: "x"}); !a.Admitted {
		t.Fatalf("retry counted against itself: %+v", a)
	}
	n := clock(t)
	*n = n.Add(10 * time.Minute) // a reservation nobody used lapses
	if b, _ = s.Admit(AdmitOptions{Seats: 1, For: "y"}); !b.Admitted {
		t.Fatalf("lapsed reservation still holds the seat: %+v", b)
	}
}

func TestRegressResourceEpochIsMonotonicAndDropIsFenced(t *testing.T) {
	s := open(t, repo(t))
	a, _ := s.Take("db", "seat", time.Minute)
	if err := s.Drop("db", "seat", a.Epoch); err != nil {
		t.Fatal(err)
	}
	b, _ := s.Take("db", "seat", time.Minute)
	if b.Epoch <= a.Epoch {
		t.Fatalf("token reused: %d then %d", a.Epoch, b.Epoch)
	}
	refused(t, s.Drop("db", "seat", a.Epoch), "lease_fenced")
	if s.Holder("db") != "seat" {
		t.Fatal("a delayed drop released the replacement lease")
	}
}

func TestRegressSplitIsAtomicAndRetrySafe(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	write(t, main, "briefs/tasks.json", "[]")
	_, err := s.Split("parent", main, []TaskRow{{Branch: "kid"}, {Branch: "kid"}}, "why")
	refused(t, err, "bad_split")
	if rows, _ := ReadTasks(main); len(rows) != 0 {
		t.Fatalf("a refused split queued %d children", len(rows))
	}
	d1, err := s.Split("parent", main, []TaskRow{{Branch: "kid1"}}, "why")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Split("parent", main, []TaskRow{{Branch: "kid2"}}, "why again"); err != nil {
		t.Fatalf("a second split by the same seat collided with the first: %v", err)
	}
	d1b, err := s.Split("parent", main, []TaskRow{{Branch: "kid1"}}, "why")
	if err != nil || d1b.ID != d1.ID {
		t.Fatalf("retry of the same split: %v %+v", err, d1b)
	}
	if rows, _ := ReadTasks(main); len(rows) != 2 {
		t.Fatalf("tasks = %+v", rows)
	}
	_, err = s.Split("other", main, []TaskRow{{Branch: "kid1"}}, "steal")
	refused(t, err, "bad_split")
}

func TestRegressFailedWakeLosesNothing(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	s.recordSession("a", hookInput{SessionID: "sid", Cwd: main, Event: "SessionStart"})
	s.recordSession("a", hookInput{SessionID: "sid", Cwd: main, Event: "Stop"})
	if _, err := s.Nudge("a", "test", "important"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir()) // no provider on PATH
	s.WakePending(WakeOptions{})
	WaitWakes(5 * time.Second)
	if notes, _ := s.Inbox("a", false); len(notes) != 1 {
		t.Fatalf("a wake that never started consumed the note: %d left", len(notes))
	}
	data, _ := os.ReadFile(s.path("wakes.jsonl"))
	var w WakeResult
	_ = json.Unmarshal(data, &w)
	if w.Exit == 0 || w.Delivered || w.Err == "" {
		t.Fatalf("failed wake recorded as success: %s", data)
	}
	// Backed off: an immediate second pass does not try again.
	if started := s.WakePending(WakeOptions{}); len(started) != 0 {
		t.Fatalf("no backoff after a failed wake: %v", started)
	}
}

func TestRegressLateStopCannotReplaceLiveSession(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	s.recordSession("a", hookInput{SessionID: "new-live", Cwd: main, Event: "SessionStart"})
	s.recordSession("a", hookInput{SessionID: "old", Cwd: main, Event: "Stop"})
	r, wakeable := s.Wakeable("a")
	if wakeable || r.SessionID != "new-live" {
		t.Fatalf("old session's late Stop took over: %+v wakeable=%v", r, wakeable)
	}
}

func tipsOf(t *testing.T, main string, names ...string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, n := range names {
		m[n] = must(t, main, "rev-parse", n)
	}
	return m
}

func TestRegressOwnFilesAfterMergedPeerMoves(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	a := branch(t, main, "a", "briefs/out/a/START.md", "own.go")
	b := branch(t, main, "b", "peer.go")
	must(t, a, "merge", "--no-ff", "-q", "-m", "inherit b", "b")
	base := must(t, main, "rev-parse", "main")
	before := s.ownFiles("a", must(t, a, "rev-parse", "HEAD"), base, tipsOf(t, main, "a", "b"))
	write(t, b, "later.go", "package a\n")
	must(t, b, "add", "-A")
	must(t, b, "commit", "-qm", "b moved")
	after := s.ownFiles("a", must(t, a, "rev-parse", "HEAD"), base, tipsOf(t, main, "a", "b"))
	for _, fs := range [][]string{before, after} {
		if strings.Contains(strings.Join(fs, ","), "peer.go") {
			t.Fatalf("a owns what it merged from b: before %v after %v", before, after)
		}
	}
}

func TestRegressOwnFilesSeesMergeOnlyEdit(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	a := branch(t, main, "a", "briefs/out/a/START.md", "own.go")
	branch(t, main, "b", "peer.go")
	must(t, a, "merge", "--no-ff", "--no-commit", "b")
	write(t, a, "merge-only.go", "package a\n")
	must(t, a, "add", "-A")
	must(t, a, "commit", "-qm", "merge with novel edit")
	fs := s.ownFiles("a", must(t, a, "rev-parse", "HEAD"), must(t, main, "rev-parse", "main"), tipsOf(t, main, "a", "b"))
	joined := strings.Join(fs, ",")
	if !strings.Contains(joined, "merge-only.go") || strings.Contains(joined, "peer.go") {
		t.Fatalf("own files = %v", fs)
	}
}

func TestRegressUnverifiedMergeIsNotAccepted(t *testing.T) {
	main := repo(t)
	s := open(t, main)
	a := branch(t, main, "a", "own.go")
	land(t, a, "a")
	theme := filepath.Join(filepath.Dir(main), "wt", "theme")
	must(t, main, "worktree", "add", "-q", theme, "-b", "theme", "main")
	// The durable state after a crash between merge and verify.
	must(t, theme, "merge", "--no-ff", "-qm", "merge before simulated crash", "a")
	r, err := s.Consolidate(theme, "git ls-files --error-unmatch no-such-file", BoardOptions{Base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if r.HeadRed == "" || len(r.Merged) != 0 {
		t.Fatalf("an unverified red merge was accepted: %+v", r)
	}
	// With a verifier that passes, the same head is accepted and recorded.
	r, err = s.Consolidate(theme, "git --version", BoardOptions{Base: "main"})
	if err != nil || r.HeadRed != "" || len(r.Skipped) != 1 {
		t.Fatalf("green retry: %v %+v", err, r)
	}
}

func TestRegressFileKeysDoNotCollide(t *testing.T) {
	if fileKey("a/b") == fileKey("a_b") || fileKey("a/b") == fileKey("a.b") {
		t.Fatalf("collision: %q %q", fileKey("a/b"), fileKey("a_b"))
	}
	s := open(t, repo(t))
	_, _ = s.Nudge("a/b", "k", "for slash")
	if n, _ := s.Inbox("a_b", false); len(n) != 0 {
		t.Fatal("a_b read a/b's inbox")
	}
}
