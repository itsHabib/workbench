package plane

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// backends returns every store the contract must hold on. The RESP backend
// joins when SWARM_TEST_REDIS names a server.
func backends(t *testing.T) map[string]func() Store {
	t.Helper()
	out := map[string]func() Store{
		"mem":  func() Store { return NewMem() },
		"file": func() Store { f, err := OpenFile(t.TempDir()); must(t, err); return f },
	}
	if addr := redisAddr(); addr != "" {
		out["resp"] = func() Store {
			r, err := OpenRESP(addr, fmt.Sprintf("t%d:", time.Now().UnixNano()))
			must(t, err)
			return r
		}
	}
	return out
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func is(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func TestContract(t *testing.T) {
	ctx := context.Background()
	for name, open := range backends(t) {
		t.Run(name, func(t *testing.T) {
			s := open()
			defer s.Close()
			must(t, s.Put(ctx, Item{Kind: "task", ID: "a", Payload: "p"}))
			must(t, s.Put(ctx, Item{Kind: "task", ID: "a", Payload: "other"})) // no-op

			g1, err := s.Claim(ctx, "task", "a", "w1", 80*time.Millisecond, "c1")
			must(t, err)
			if g1.Epoch != 1 || g1.Payload != "p" {
				t.Fatalf("grant %+v", g1)
			}
			again, err := s.Claim(ctx, "task", "a", "w1", time.Minute, "c1")
			must(t, err)
			if again.Epoch != g1.Epoch {
				t.Fatalf("retry of a claim acted twice: %+v", again)
			}
			_, err = s.Claim(ctx, "task", "a", "w2", time.Minute, "c2")
			is(t, err, ErrHeld)

			time.Sleep(120 * time.Millisecond) // w1's lease lapses; w2 takes over
			g2, err := s.Claim(ctx, "task", "a", "w2", time.Minute, "c3")
			must(t, err)
			if g2.Epoch != 2 {
				t.Fatalf("takeover epoch %d", g2.Epoch)
			}
			// The paused holder wakes and tries to finish: fenced, not accepted.
			_, err = s.Commit(ctx, g1, "w1-result", "k1", nil)
			is(t, err, ErrFenced)
			_, err = s.Renew(ctx, g1, time.Minute)
			is(t, err, ErrFenced)
			is(t, s.Release(ctx, g1), ErrFenced)

			r, err := s.Commit(ctx, g2, "w2-result", "k2", []Item{{Kind: "event", ID: "a.done"}, {Kind: "task", ID: "a.child"}})
			must(t, err)
			if !r.Accepted || r.Emitted != 2 {
				t.Fatalf("receipt %+v", r)
			}
			// A retry with the same key returns the same receipt and emits nothing new.
			r2, err := s.Commit(ctx, g2, "different", "k2", []Item{{Kind: "event", ID: "a.done2"}})
			must(t, err)
			if !r2.Accepted || r2.Result != "w2-result" {
				t.Fatalf("retry receipt %+v", r2)
			}
			if _, err := s.Get(ctx, "event", "a.done2"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("a replayed commit emitted again: %v", err)
			}
			// Anyone else finishing now learns who won.
			lose, err := s.Commit(ctx, g2, "late", "k9", nil)
			is(t, err, ErrDone)
			if lose.Accepted || lose.Winner != "w2" || lose.Result != "w2-result" {
				t.Fatalf("loser receipt %+v", lose)
			}
			_, err = s.Claim(ctx, "task", "a", "w3", time.Minute, "c4")
			is(t, err, ErrDone)

			// Emitted items exist, pending, and are claimable in order.
			ev, err := s.ClaimNext(ctx, "event", "watcher", time.Minute, "e1")
			must(t, err)
			if ev.ID != "a.done" {
				t.Fatalf("event %+v", ev)
			}
			_, err = s.ClaimNext(ctx, "event", "watcher2", time.Minute, "e2")
			is(t, err, ErrNone)

			// Release returns an item to the pool and never rewinds the epoch.
			must(t, s.Put(ctx, Item{Kind: "seat", ID: "s1"}))
			a, err := s.ClaimNext(ctx, "seat", "w1", time.Minute, "")
			must(t, err)
			must(t, s.Release(ctx, a))
			b, err := s.ClaimNext(ctx, "seat", "w2", time.Minute, "")
			must(t, err)
			if b.Epoch <= a.Epoch {
				t.Fatalf("epoch rewound across release: %d then %d", a.Epoch, b.Epoch)
			}
			is(t, s.Release(ctx, a), ErrFenced) // a delayed release cannot free w2's seat

			h, err := s.History(ctx)
			must(t, err)
			if rep := Check(h, "task"); len(rep.Violations) != 0 {
				t.Fatalf("checker found violations in a correct run: %+v", rep.Violations)
			}
		})
	}
}

// TestManyClaimantsOneWinner races workers at every item; the store's own
// history must show exactly one acceptance each.
func TestManyClaimantsOneWinner(t *testing.T) {
	ctx := context.Background()
	for name, open := range backends(t) {
		t.Run(name, func(t *testing.T) {
			s := open()
			defer s.Close()
			const tasks, workers = 40, 8
			for i := 0; i < tasks; i++ {
				must(t, s.Put(ctx, Item{Kind: "task", ID: fmt.Sprintf("t%02d", i)}))
			}
			var wg sync.WaitGroup
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func(w int) {
					defer wg.Done()
					inc := fmt.Sprintf("w%d", w)
					for {
						g, err := s.ClaimNext(ctx, "task", inc, 50*time.Millisecond, "")
						if errors.Is(err, ErrNone) {
							return
						}
						if errors.Is(err, ErrHeld) || errors.Is(err, ErrDone) {
							continue
						}
						if err != nil {
							t.Errorf("claim: %v", err)
							return
						}
						if w%3 == 0 {
							time.Sleep(70 * time.Millisecond) // overstay the lease on purpose
						}
						_, _ = s.Commit(ctx, g, inc, "", nil)
					}
				}(w)
			}
			wg.Wait()
			h, _ := s.History(ctx)
			rep := Check(h, "task")
			if !rep.OK() || rep.Accepted != tasks {
				t.Fatalf("accepted %d of %d; unfinished %v; violations %+v", rep.Accepted, tasks, rep.Unfinished, rep.Violations)
			}
		})
	}
}

// TestCheckerCatchesBrokenStores is the test of the test: each mutant
// switches off one safety check, and the checker must name the invariant it
// breaks. A checker that cannot fail proves nothing.
func TestCheckerCatchesBrokenStores(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		broken Broken
		rule   string
	}{
		{"no epoch check", Broken{NoEpochCheck: true}, "current-lease"},
		// Done and pending checks back each other up: a second acceptance needs both gone.
		{"no done or pending check", Broken{NoDoneCheck: true, NoPendingCheck: true}, "accepted-once"},
		{"no pending check", Broken{NoPendingCheck: true}, "no-claim-after"},
		{"epoch reuse", Broken{EpochReuse: true}, "epochs-grow"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := NewMem()
			m.Broken = c.broken
			now := time.Now().UTC()
			m.Now = func() time.Time { return now }
			must(t, m.Put(ctx, Item{Kind: "task", ID: "a"}))
			g1, _ := m.Claim(ctx, "task", "a", "w1", time.Second, "")
			if c.broken.EpochReuse {
				_ = m.Release(ctx, g1)
				_, _ = m.Claim(ctx, "task", "a", "w2", time.Second, "")
			}
			now = now.Add(2 * time.Second)
			g2, _ := m.Claim(ctx, "task", "a", "w2", time.Second, "")
			_, _ = m.Commit(ctx, g1, "one", "", nil) // the stale holder finishes first
			_, _ = m.Commit(ctx, g2, "two", "", nil)
			_, _ = m.Commit(ctx, g2, "two again", "", nil)
			if g3, err := m.Claim(ctx, "task", "a", "w3", time.Second, ""); err == nil {
				_, _ = m.Commit(ctx, g3, "three", "", nil)
			}
			h, _ := m.History(ctx)
			rep := Check(h)
			for _, v := range rep.Violations {
				if v.Rule == c.rule {
					return
				}
			}
			t.Fatalf("checker missed %s; found %+v", c.rule, rep.Violations)
		})
	}
}

// TestFileCrashBetweenHistoryAndState: a step that wrote its history lines
// and died before its state must leave no trace, and the next step must
// not inherit its sequence numbers.
func TestFileCrashBetweenHistoryAndState(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f, err := OpenFile(dir)
	must(t, err)
	must(t, f.Put(ctx, Item{Kind: "task", ID: "a"}))
	before, _ := f.History(ctx)
	// The durable state of a crash after the history append: an orphan line.
	orphan := Event{Seq: int64(len(before)) + 1, Op: "commit", Kind: "task", ID: "a", OK: true, By: "ghost", Epoch: 9}
	must(t, appendSynced(f.path("plane.log.jsonl"), []Event{orphan}))
	g, err := f.Claim(ctx, "task", "a", "w1", time.Minute, "")
	must(t, err)
	_, err = f.Commit(ctx, g, "real", "", nil)
	must(t, err)
	h, _ := f.History(ctx)
	for _, e := range h {
		if e.By == "ghost" {
			t.Fatalf("uncommitted history survived: %+v", e)
		}
	}
	if rep := Check(h, "task"); !rep.OK() || rep.Accepted != 1 {
		t.Fatalf("after recovery: %+v", rep)
	}
}

// TestRolesComeFromTheStore: identical peers tell themselves apart only by
// what the store grants them, and every run has builders however few peers
// there are.
func TestRolesComeFromTheStore(t *testing.T) {
	ctx := context.Background()
	m := NewMem()
	must(t, m.Put(ctx, Item{Kind: "role", ID: "watcher0"}))
	must(t, m.Put(ctx, Item{Kind: "role", ID: "watcher1"}))
	watchers := 0
	for i := 0; i < 5; i++ {
		inc := fmt.Sprintf("p%d", i)
		if g, err := m.ClaimNext(ctx, "role", inc, time.Minute, inc+":role"); err == nil {
			if r, err := m.Commit(ctx, g, inc, inc+":role:commit", nil); err == nil && r.Accepted {
				watchers++
			}
		}
	}
	if watchers != 2 {
		t.Fatalf("%d of 5 peers became watchers, want exactly 2", watchers)
	}
}
