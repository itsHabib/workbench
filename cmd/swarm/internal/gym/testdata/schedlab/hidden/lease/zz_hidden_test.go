package lease

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"schedlab/clock"
	"schedlab/errs"
)

func TestHiddenAcquireAndEpochs(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1000))
	s := New(f)
	l, err := s.Acquire("job/a", "w1", 5*time.Second)
	if err != nil || l != (Lease{Key: "job/a", Holder: "w1", Epoch: 1, ExpiresMs: 6000}) {
		t.Fatalf("%+v %v", l, err)
	}
	if got, ok := s.Get("job/a"); !ok || got != l {
		t.Fatalf("%+v %v", got, ok)
	}
	if s.Epoch("job/a") != 1 || s.Epoch("other") != 0 {
		t.Fatalf("epochs %d %d", s.Epoch("job/a"), s.Epoch("other"))
	}
	if _, err := s.Acquire("job/a", "w2", time.Second); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("other holder: %v", err)
	}
	f.Advance(2 * time.Second)
	renewed, err := s.Acquire("job/a", "w1", 5*time.Second)
	if err != nil || renewed.Epoch != 1 || renewed.ExpiresMs != 8000 {
		t.Fatalf("renew: %+v %v", renewed, err)
	}
	if err := s.Release("job/a", "w1", 1); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("job/a"); ok {
		t.Fatal("still held after release")
	}
	next, err := s.Acquire("job/a", "w2", time.Second)
	if err != nil || next.Epoch != 2 || s.Epoch("job/a") != 2 {
		t.Fatalf("epoch after release: %+v %v", next, err)
	}
}

func TestHiddenInvalid(t *testing.T) {
	s := New(clock.NewFake(time.UnixMilli(0)))
	for _, c := range []struct {
		key, holder string
		ttl         time.Duration
	}{{"", "w", time.Second}, {"k", "", time.Second}, {"k", "w", 0}, {"k", "w", -time.Second}, {"k", "w", 999 * time.Microsecond}} {
		if _, err := s.Acquire(c.key, c.holder, c.ttl); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	if s.Epoch("k") != 0 {
		t.Fatal("a refused acquire used an epoch")
	}
	if l, err := s.Acquire("k", "w", time.Millisecond); err != nil || l.ExpiresMs != 1 {
		t.Fatalf("1ms ttl: %+v %v", l, err)
	}
}

func TestHiddenExpiry(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(0))
	s := New(f)
	_, _ = s.Acquire("k", "w1", 100*time.Millisecond)
	f.Advance(99 * time.Millisecond)
	if _, ok := s.Get("k"); !ok {
		t.Fatal("expired early")
	}
	if err := s.Check("k", "w1", 1); err != nil {
		t.Fatal(err)
	}
	f.Advance(time.Millisecond)
	if _, ok := s.Get("k"); ok {
		t.Fatal("live at expiry")
	}
	if err := s.Check("k", "w1", 1); !errors.Is(err, errs.ErrExpired) {
		t.Fatalf("check after expiry: %v", err)
	}
	if err := s.Release("k", "w1", 1); !errors.Is(err, errs.ErrExpired) {
		t.Fatalf("release after expiry: %v", err)
	}
	l, err := s.Acquire("k", "w1", time.Second)
	if err != nil || l.Epoch != 2 || l.ExpiresMs != 1100 {
		t.Fatalf("re-acquire by the same holder after expiry: %+v %v", l, err)
	}
	l2, err := s.Acquire("k", "w2", time.Second)
	if !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("%+v %v", l2, err)
	}
	f.Advance(time.Second)
	l2, err = s.Acquire("k", "w2", time.Second)
	if err != nil || l2.Epoch != 3 {
		t.Fatalf("other holder after expiry: %+v %v", l2, err)
	}
}

func TestHiddenCheckAndReleaseFencing(t *testing.T) {
	s := New(clock.NewFake(time.UnixMilli(0)))
	l, _ := s.Acquire("k", "w1", time.Second)
	if err := s.Check("k", "w2", l.Epoch); !errors.Is(err, errs.ErrState) {
		t.Fatalf("wrong holder: %v", err)
	}
	if err := s.Check("k", "w1", l.Epoch+1); !errors.Is(err, errs.ErrState) {
		t.Fatalf("wrong epoch: %v", err)
	}
	if err := s.Check("nope", "w1", 1); !errors.Is(err, errs.ErrExpired) {
		t.Fatalf("unknown key: %v", err)
	}
	if err := s.Release("k", "w2", l.Epoch); !errors.Is(err, errs.ErrState) {
		t.Fatalf("release by other: %v", err)
	}
	if err := s.Release("k", "w1", 0); !errors.Is(err, errs.ErrState) {
		t.Fatalf("release with stale epoch: %v", err)
	}
	if _, ok := s.Get("k"); !ok {
		t.Fatal("a refused release dropped the lease")
	}
	if err := s.Release("k", "w1", l.Epoch); err != nil {
		t.Fatal(err)
	}
	if err := s.Release("k", "w1", l.Epoch); !errors.Is(err, errs.ErrExpired) {
		t.Fatalf("double release: %v", err)
	}
}

func TestHiddenEpochsNeverGoBack(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(0))
	s := New(f)
	for i := int64(1); i <= 5; i++ {
		l, err := s.Acquire("k", "w", time.Second)
		if err != nil || l.Epoch != i {
			t.Fatalf("round %d: %+v %v", i, l, err)
		}
		if i%2 == 0 {
			_ = s.Release("k", "w", l.Epoch)
			continue
		}
		f.Advance(time.Second)
	}
	if s.Epoch("k") != 5 {
		t.Fatalf("epoch %d", s.Epoch("k"))
	}
}

func TestHiddenConcurrentSingleWinner(t *testing.T) {
	s := New(clock.NewFake(time.UnixMilli(0)))
	var wins, conflicts atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := s.Acquire("hot", "w"+string(rune('a'+i%26))+string(rune('a'+i/26)), time.Hour)
			switch {
			case err == nil:
				wins.Add(1)
			case errors.Is(err, errs.ErrConflict):
				conflicts.Add(1)
			default:
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if wins.Load() != 1 || conflicts.Load() != 29 || s.Epoch("hot") != 1 {
		t.Fatalf("wins=%d conflicts=%d epoch=%d", wins.Load(), conflicts.Load(), s.Epoch("hot"))
	}
}
