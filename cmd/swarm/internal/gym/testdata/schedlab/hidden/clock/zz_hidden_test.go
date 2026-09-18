package clock

import (
	"sync"
	"testing"
	"time"
)

func TestHiddenFakeStandsStill(t *testing.T) {
	start := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	f := NewFake(start)
	var c Clock = f
	for i := 0; i < 3; i++ {
		if !c.Now().Equal(start) {
			t.Fatalf("moved to %v", c.Now())
		}
	}
	if Millis(f) != start.UnixMilli() {
		t.Fatalf("millis %d", Millis(f))
	}
}

func TestHiddenAdvanceAndSet(t *testing.T) {
	start := time.UnixMilli(1000)
	f := NewFake(start)
	if got := f.Advance(1500 * time.Millisecond); !got.Equal(time.UnixMilli(2500)) || !f.Now().Equal(got) {
		t.Fatalf("advance gave %v now %v", got, f.Now())
	}
	if got := f.Advance(0); !got.Equal(time.UnixMilli(2500)) {
		t.Fatalf("advance 0 gave %v", got)
	}
	f.Set(time.UnixMilli(10))
	if Millis(f) != 10 {
		t.Fatalf("set backwards: %d", Millis(f))
	}
	f.Set(time.UnixMilli(99999))
	if Millis(f) != 99999 {
		t.Fatalf("set forwards: %d", Millis(f))
	}
}

func TestHiddenNegativeAdvancePanics(t *testing.T) {
	f := NewFake(time.UnixMilli(1000))
	defer func() {
		if recover() == nil {
			t.Fatal("no panic")
		}
		if Millis(f) != 1000 {
			t.Fatalf("moved to %d", Millis(f))
		}
	}()
	f.Advance(-time.Millisecond)
}

func TestHiddenSystemAndConcurrentFake(t *testing.T) {
	before := time.Now()
	got := System().Now()
	if got.Before(before) || time.Since(got) > time.Minute {
		t.Fatalf("system clock %v", got)
	}
	f := NewFake(time.UnixMilli(0))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.Advance(time.Millisecond)
			_ = f.Now()
		}()
	}
	wg.Wait()
	if Millis(f) != 50 {
		t.Fatalf("millis %d", Millis(f))
	}
}
