package jobs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (Store, *time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	return Store{Dir: filepath.Join(t.TempDir(), "jobs"), Now: func() time.Time { return now }}, &now
}
func run(t *testing.T, s Store, r Request) Job {
	t.Helper()
	v, e := s.Execute(r)
	if e != nil {
		t.Fatal(e)
	}
	return v.(Job)
}
func seed(t *testing.T, s Store, id string) Job {
	return run(t, s, Request{Op: "submit", ID: id, Brief: "build " + id})
}
func claim(t *testing.T, s Store, id, worker, key string) Job {
	return run(t, s, Request{Op: "claim", ID: id, Worker: worker, Key: key, TTLSeconds: 10})
}
func last(j Job) Attempt { return j.Attempts[len(j.Attempts)-1] }
func wantErr(t *testing.T, s Store, r Request, want error) {
	t.Helper()
	_, e := s.Execute(r)
	if !errors.Is(e, want) {
		t.Fatalf("%s: got %v want %v", r.Op, e, want)
	}
}
func TestConcurrentClaims(t *testing.T) {
	s, _ := fixture(t)
	seed(t, s, "one")
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Execute(Request{Op: "claim", ID: "one", Worker: fmt.Sprint(i), Key: fmt.Sprint(i), TTLSeconds: 10})
			if err == nil {
				mu.Lock()
				wins++
				mu.Unlock()
				return
			}
			if !errors.Is(err, ErrConflict) {
				t.Errorf("claim: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("accepted %d claims", wins)
	}
}
func TestExpiryAndReplay(t *testing.T) {
	s, now := fixture(t)
	seed(t, s, "one")
	j := claim(t, s, "one", "worker", "request")
	a := last(j)
	replay := claim(t, s, "one", "worker", "request")
	if last(replay).Token != a.Token || len(replay.Attempts) != 1 {
		t.Fatal("claim replay changed attempt")
	}
	wantErr(t, s, Request{Op: "claim", ID: "one", Worker: "other", Key: "request", TTLSeconds: 10}, ErrConflict)
	*now = now.Add(10 * time.Second)
	for _, op := range []string{"renew", "complete"} {
		wantErr(t, s, Request{Op: op, ID: "one", Worker: "worker", Token: a.Token, TTLSeconds: 10, Result: "result"}, ErrConflict)
	}
	wantErr(t, s, Request{Op: "claim", ID: "one", Worker: "worker", Key: "request", TTLSeconds: 10}, ErrConflict)
	j = claim(t, s, "one", "replacement", "request2")
	if len(j.Attempts) != 2 || last(j).Token == a.Token {
		t.Fatal("missing new attempt")
	}
	wantErr(t, s, Request{Op: "complete", ID: "one", Worker: "worker", Token: a.Token, Result: "late"}, ErrConflict)
}
func TestReviewTransitions(t *testing.T) {
	s, _ := fixture(t)
	seed(t, s, "one")
	a := last(claim(t, s, "one", "worker", "key"))
	done := Request{Op: "complete", ID: "one", Worker: a.Worker, Token: a.Token, Result: "sha:abc"}
	run(t, s, done)
	run(t, s, done)
	changed := done
	changed.Result = "sha:def"
	wantErr(t, s, changed, ErrConflict)
	retry := Request{Op: "retry", ID: "one", Token: a.Token, Evidence: "test failed"}
	run(t, s, retry)
	run(t, s, retry)
	wantErr(t, s, done, ErrConflict)
	wantErr(t, s, Request{Op: "accept", ID: "one", Token: a.Token, Evidence: "ok"}, ErrConflict)
	a = last(claim(t, s, "one", "worker", "key2"))
	done.Token = a.Token
	run(t, s, done)
	accept := Request{Op: "accept", ID: "one", Token: a.Token, Evidence: "tests passed"}
	run(t, s, accept)
	run(t, s, accept)
	accept.Evidence = "changed"
	wantErr(t, s, accept, ErrConflict)
	wantErr(t, s, Request{Op: "retry", ID: "one", Token: a.Token, Evidence: "retry"}, ErrConflict)
}
func TestQueueWorkerLimitAndMetrics(t *testing.T) {
	s, now := fixture(t)
	seed(t, s, "first")
	*now = now.Add(time.Second)
	seed(t, s, "second")
	j := claim(t, s, "", "w", "first-claim")
	if j.ID != "first" {
		t.Fatal(j.ID)
	}
	wantErr(t, s, Request{Op: "claim", Worker: "w", Key: "second-claim", TTLSeconds: 10}, ErrConflict)
	if j = claim(t, s, "", "other", "second-claim"); j.ID != "second" {
		t.Fatal(j.ID)
	}
	*now = now.Add(11 * time.Second)
	v, e := s.Execute(Request{Op: "metrics"})
	if e != nil {
		t.Fatal(e)
	}
	m := v.(Metrics)
	if m.ExpiredEligible != 2 || m.Attempts != 2 || m.OldestQueueWaitSeconds != 1 {
		t.Fatalf("%+v", m)
	}
	j = claim(t, s, "", "w", "third-claim")
	if j.ID != "first" || len(j.Attempts) != 2 {
		t.Fatal(j)
	}
}
func TestReadOnlyAndCorruption(t *testing.T) {
	s, _ := fixture(t)
	for _, op := range []string{"list", "metrics"} {
		if _, e := s.Execute(Request{Op: op}); e != nil {
			t.Fatal(e)
		}
	}
	wantErr(t, s, Request{Op: "get", ID: "x"}, ErrNotFound)
	if _, e := os.Stat(s.Dir); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("reads created files")
	}
	seed(t, s, "x")
	path := filepath.Join(s.Dir, "jobs.json")
	for _, bad := range []string{"{", `null`, `{}`, `{"version":1,"jobs":[{"id":"x","brief":"x","state":"running"}]}`} {
		if e := os.WriteFile(path, []byte(bad), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := s.Execute(Request{Op: "list"}); e == nil {
			t.Fatal("read accepted corrupt data")
		}
		if _, e := s.Execute(Request{Op: "submit", ID: "y", Brief: "y"}); e == nil {
			t.Fatal("mutation accepted corrupt data")
		}
		b, _ := os.ReadFile(path)
		if string(b) != bad {
			t.Fatal("corrupt store overwritten")
		}
	}
}
func TestSubmitRenewAndRejectedInputs(t *testing.T) {
	s, now := fixture(t)
	seed(t, s, "one")
	seed(t, s, "one")
	wantErr(t, s, Request{Op: "submit", ID: "one", Brief: "changed"}, ErrConflict)
	for _, ttl := range []int{0, -1, 3601} {
		wantErr(t, s, Request{Op: "claim", ID: "one", Worker: "w", Key: "k", TTLSeconds: ttl}, ErrInvalid)
	}
	a := last(claim(t, s, "one", "w", "k"))
	*now = now.Add(5 * time.Second)
	j := run(t, s, Request{Op: "renew", ID: "one", Worker: "w", Token: a.Token, TTLSeconds: 20})
	if !last(j).ExpiresAt.Equal(now.Add(20 * time.Second)) {
		t.Fatal(j)
	}
	wantErr(t, s, Request{Op: "complete", ID: "one", Worker: "w", Token: a.Token}, ErrInvalid)
	wantErr(t, s, Request{Op: "accept", ID: "one", Token: a.Token, Evidence: "ok"}, ErrConflict)
	*now = now.Add(20 * time.Second)
	j = run(t, s, Request{Op: "retry", ID: "one", Token: a.Token, Evidence: "recover"})
	if j.State != "queued" {
		t.Fatal(j)
	}
}

func TestSemanticCorruption(t *testing.T) {
	cases := map[string]func(*snapshot){
		"reported missing result": func(d *snapshot) { d.Jobs[0].State = "reported" },
		"accepted missing evidence": func(d *snapshot) {
			j := &d.Jobs[0]
			j.State = "accepted"
			j.Attempts[0].Result = "x"
			j.Attempts[0].CompletedAt = &j.UpdatedAt
		},
		"queued active attempt": func(d *snapshot) { d.Jobs[0].State = "queued" },
		"invalid ttl":           func(d *snapshot) { d.Jobs[0].Attempts[0].TTLSeconds = 0 },
		"duplicate live worker": func(d *snapshot) {
			j := d.Jobs[0]
			j.ID = "other"
			j.Attempts = append([]Attempt(nil), j.Attempts...)
			j.Attempts[0].Token = "other"
			j.Attempts[0].Key = "other"
			d.Jobs = append(d.Jobs, j)
		},
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			s, _ := fixture(t)
			seed(t, s, "one")
			claim(t, s, "one", "worker", "key")
			d, e := s.read()
			if e != nil {
				t.Fatal(e)
			}
			corrupt(&d)
			b, e := json.Marshal(d)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(s.Dir, "jobs.json"), b, 0600); e != nil {
				t.Fatal(e)
			}
			if _, e = s.Execute(Request{Op: "list"}); e == nil {
				t.Fatal("accepted corrupt snapshot")
			}
		})
	}
}

func TestWorkerReuseRetiresExpiredClaimAcrossClockRollback(t *testing.T) {
	s, now := fixture(t)
	start := *now
	seed(t, s, "a")
	seed(t, s, "b")
	a := last(claim(t, s, "a", "worker", "a-key"))
	*now = start.Add(11 * time.Second)
	claim(t, s, "b", "worker", "b-key")
	*now = start.Add(5 * time.Second)
	for _, op := range []string{"list", "metrics"} {
		if _, err := s.Execute(Request{Op: op}); err != nil {
			t.Fatal(err)
		}
	}
	j := run(t, s, Request{Op: "get", ID: "a"})
	if j.State != "queued" || last(j).RetriedAt == nil || !j.QueuedAt.Equal(a.ExpiresAt) {
		t.Fatalf("expired claim not retired: %+v", j)
	}
	wantErr(t, s, Request{Op: "complete", ID: "a", Worker: "worker", Token: a.Token, Result: "late"}, ErrConflict)
	wantErr(t, s, Request{Op: "renew", ID: "a", Worker: "worker", Token: a.Token, TTLSeconds: 10}, ErrConflict)
	wantErr(t, s, Request{Op: "claim", ID: "a", Worker: "worker", Key: "a-key", TTLSeconds: 10}, ErrConflict)
}
func TestRenewNeverShortensLease(t *testing.T) {
	s, now := fixture(t)
	seed(t, s, "a")
	a := last(claim(t, s, "a", "w", "key"))
	for _, offset := range []time.Duration{time.Second, -5 * time.Second} {
		*now = a.StartedAt.Add(offset)
		j := run(t, s, Request{Op: "renew", ID: "a", Worker: "w", Token: a.Token, TTLSeconds: 1})
		if !last(j).ExpiresAt.Equal(a.ExpiresAt) {
			t.Fatal("renew shortened lease")
		}
	}
}
