package worker

import (
	"errors"
	"sync"
	"testing"
	"time"

	"schedlab/clock"
	"schedlab/errs"
	"schedlab/lease"
	"schedlab/queue"
)

type rig struct {
	f  *clock.Fake
	q  *queue.Queue
	ls *lease.Store
}

func newRig(t *testing.T, h Handler, ttl time.Duration) (*Pool, rig) {
	t.Helper()
	f := clock.NewFake(time.UnixMilli(1000))
	r := rig{f: f, q: queue.New(f), ls: lease.New(f)}
	p, err := New(f, r.q, r.ls, ttl, h)
	if err != nil {
		t.Fatal(err)
	}
	return p, r
}

func echo(item queue.Item) (string, error) { return "out:" + item.Payload, nil }

func TestHiddenNewRejects(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(0))
	q, ls := queue.New(f), lease.New(f)
	for name, fn := range map[string]func() (*Pool, error){
		"nil queue":   func() (*Pool, error) { return New(f, nil, ls, time.Second, echo) },
		"nil leases":  func() (*Pool, error) { return New(f, q, nil, time.Second, echo) },
		"nil handler": func() (*Pool, error) { return New(f, q, ls, time.Second, nil) },
		"zero ttl":    func() (*Pool, error) { return New(f, q, ls, 0, echo) },
		"sub-ms ttl":  func() (*Pool, error) { return New(f, q, ls, 500*time.Microsecond, echo) },
	} {
		if _, err := fn(); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := New(f, q, ls, time.Millisecond, echo); err != nil {
		t.Fatal(err)
	}
}

func TestHiddenRunClaimsUnderLease(t *testing.T) {
	var seen []queue.Item
	p, r := newRig(t, func(item queue.Item) (string, error) { seen = append(seen, item); return echo(item) }, 5*time.Second)
	if _, ok, err := p.Run("w1"); ok || err != nil {
		t.Fatalf("empty queue: %v %v", ok, err)
	}
	if _, _, err := p.Run(""); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("empty name: %v", err)
	}
	_ = r.q.Push("run1/build", 3, 0, "build")
	res, ok, err := p.Run("w1")
	if err != nil || !ok {
		t.Fatalf("%+v %v %v", res, ok, err)
	}
	want := Result{ItemID: "run1/build", Worker: "w1", Epoch: 1, OK: true, Output: "out:build"}
	if res != want {
		t.Fatalf("%+v, want %+v", res, want)
	}
	if len(seen) != 1 || seen[0] != (queue.Item{ID: "run1/build", Priority: 3, ReadyMs: 1000, Payload: "build"}) {
		t.Fatalf("handler saw %+v", seen)
	}
	l, live := r.ls.Get("run1/build")
	if !live || l.Holder != "w1" || l.Epoch != 1 || l.ExpiresMs != 6000 {
		t.Fatalf("lease after run: %+v %v", l, live)
	}
	if r.q.Len() != 0 {
		t.Fatal("item still queued")
	}
}

func TestHiddenHandlerFailure(t *testing.T) {
	p, r := newRig(t, func(item queue.Item) (string, error) { return "partial", errors.New("boom") }, time.Second)
	_ = r.q.Push("a", 0, 0, "")
	res, ok, err := p.Run("w9")
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if res != (Result{ItemID: "a", Worker: "w9", Epoch: 1, OK: false, Output: "partial", Msg: "boom"}) {
		t.Fatalf("%+v", res)
	}
	if _, live := r.ls.Get("a"); !live {
		t.Fatal("lease dropped on failure")
	}
}

func TestHiddenLeaseConflictPushesBack(t *testing.T) {
	calls := 0
	p, r := newRig(t, func(item queue.Item) (string, error) { calls++; return echo(item) }, time.Second)
	_, _ = r.ls.Acquire("held", "other", time.Hour)
	_ = r.q.Push("held", 7, 0, "payload")
	res, ok, err := p.Run("w1")
	if !errors.Is(err, errs.ErrConflict) || ok || res != (Result{}) || calls != 0 {
		t.Fatalf("%+v %v %v calls=%d", res, ok, err, calls)
	}
	it, queued := r.q.Peek()
	if !queued || it.ID != "held" || it.Priority != 7 || it.Payload != "payload" || it.ReadyMs != 1000 || r.q.Len() != 1 {
		t.Fatalf("pushed back as %+v %v", it, queued)
	}
	r.f.Advance(time.Hour)
	res, ok, err = p.Run("w1")
	if err != nil || !ok || res.Epoch != 2 || res.Output != "out:payload" || calls != 1 {
		t.Fatalf("after the other lease expired: %+v %v %v", res, ok, err)
	}
}

func TestHiddenEpochFollowsClaims(t *testing.T) {
	p, r := newRig(t, echo, time.Second)
	for want := int64(1); want <= 3; want++ {
		_ = r.q.Push("k", 0, 0, "")
		res, ok, err := p.Run("w1")
		if err != nil || !ok || res.Epoch != want {
			t.Fatalf("claim %d: %+v %v %v", want, res, ok, err)
		}
		if err := r.ls.Release("k", "w1", res.Epoch); err != nil {
			t.Fatal(err)
		}
	}
	_ = r.q.Push("k", 0, 0, "")
	res, _, _ := p.Run("w2")
	if res.Epoch != 4 || res.Worker != "w2" || r.ls.Epoch("k") != 4 {
		t.Fatalf("%+v epoch=%d", res, r.ls.Epoch("k"))
	}
}

func TestHiddenDrain(t *testing.T) {
	p, r := newRig(t, echo, time.Second)
	for _, id := range []string{"a", "b", "c", "d"} {
		_ = r.q.Push(id, 0, 0, id)
	}
	_ = r.q.Push("later", 9, time.Minute, "")
	got, err := p.Drain("w1", 2)
	if err != nil || len(got) != 2 || got[0].ItemID != "a" || got[1].ItemID != "b" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = p.Drain("w1", 0)
	if err != nil || len(got) != 2 || got[0].ItemID != "c" || got[1].ItemID != "d" || r.q.Len() != 1 {
		t.Fatalf("%+v %v len=%d", got, err, r.q.Len())
	}
	got, err = p.Drain("w1", -1)
	if err != nil || len(got) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
	_ = r.q.Push("e", 0, 0, "")
	_, _ = r.ls.Acquire("f", "other", time.Hour)
	_ = r.q.Push("f", 0, 0, "")
	_ = r.q.Push("g", 0, 0, "")
	got, err = p.Drain("w1", 0)
	if !errors.Is(err, errs.ErrConflict) || len(got) != 1 || got[0].ItemID != "e" || r.q.Len() != 3 {
		t.Fatalf("%+v %v len=%d", got, err, r.q.Len())
	}
	var wg sync.WaitGroup
	for _, name := range []string{"x", "y", "z"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_, _, _ = p.Run(name)
		}(name)
	}
	wg.Wait()
}
