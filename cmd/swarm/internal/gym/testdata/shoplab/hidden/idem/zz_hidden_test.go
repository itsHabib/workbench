package idem

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHiddenReplay(t *testing.T) {
	s := New(time.Now, time.Hour)
	calls := 0
	fn := func() (string, error) { calls++; return "first", nil }
	reply, replayed, err := s.Do("k", fn)
	if reply != "first" || replayed || err != nil {
		t.Fatalf("first: %q %v %v", reply, replayed, err)
	}
	reply, replayed, err = s.Do("k", func() (string, error) { calls++; return "second", nil })
	if reply != "first" || !replayed || err != nil || calls != 1 {
		t.Fatalf("second: %q %v %v calls=%d", reply, replayed, err, calls)
	}
	if s.Len() != 1 {
		t.Fatalf("len %d", s.Len())
	}
}

func TestHiddenFailureIsNotRecorded(t *testing.T) {
	s := New(time.Now, time.Hour)
	boom := errors.New("boom")
	reply, replayed, err := s.Do("k", func() (string, error) { return "partial", boom })
	if reply != "partial" || replayed || !errors.Is(err, boom) || s.Len() != 0 {
		t.Fatalf("%q %v %v len=%d", reply, replayed, err, s.Len())
	}
	reply, replayed, err = s.Do("k", func() (string, error) { return "ok", nil })
	if reply != "ok" || replayed || err != nil {
		t.Fatalf("retry: %q %v %v", reply, replayed, err)
	}
}

func TestHiddenEmptyKeyAlwaysRuns(t *testing.T) {
	s := New(time.Now, time.Hour)
	calls := 0
	for i := 0; i < 3; i++ {
		_, replayed, _ := s.Do("", func() (string, error) { calls++; return "x", nil })
		if replayed {
			t.Fatal("empty key replayed")
		}
	}
	if calls != 3 || s.Len() != 0 {
		t.Fatalf("calls=%d len=%d", calls, s.Len())
	}
}

func TestHiddenExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	s := New(func() time.Time { return now }, 10*time.Second)
	one := func() (string, error) { return "one", nil }
	two := func() (string, error) { return "two", nil }
	_, _, _ = s.Do("k", one)
	now = now.Add(10*time.Second - time.Nanosecond)
	if r, replayed, _ := s.Do("k", two); r != "one" || !replayed {
		t.Fatalf("just before expiry: %q %v", r, replayed)
	}
	now = now.Add(time.Nanosecond)
	if s.Len() != 0 {
		t.Fatalf("len %d at expiry", s.Len())
	}
	if r, replayed, _ := s.Do("k", two); r != "two" || replayed {
		t.Fatalf("at expiry: %q %v", r, replayed)
	}
	forever := New(func() time.Time { return now }, 0)
	_, _, _ = forever.Do("k", one)
	now = now.Add(1000 * time.Hour)
	if r, replayed, _ := forever.Do("k", two); r != "one" || !replayed {
		t.Fatalf("ttl 0: %q %v", r, replayed)
	}
}

func TestHiddenConcurrentSingleFlight(t *testing.T) {
	s := New(time.Now, time.Hour)
	var calls, replays atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, replayed, err := s.Do("same", func() (string, error) { calls.Add(1); return "v", nil })
			if r != "v" || err != nil {
				t.Errorf("%q %v", r, err)
			}
			if replayed {
				replays.Add(1)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 || replays.Load() != 39 {
		t.Fatalf("calls=%d replays=%d", calls.Load(), replays.Load())
	}
}
