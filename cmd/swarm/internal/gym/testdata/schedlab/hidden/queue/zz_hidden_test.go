package queue

import (
	"errors"
	"sync"
	"testing"
	"time"

	"schedlab/clock"
	"schedlab/errs"
)

func TestHiddenPushPop(t *testing.T) {
	q := New(clock.NewFake(time.UnixMilli(500)))
	if err := q.Push("a", 1, 0, "pa"); err != nil {
		t.Fatal(err)
	}
	if !q.Has("a") || q.Has("b") || q.Len() != 1 || q.Ready() != 1 {
		t.Fatalf("has=%v len=%d ready=%d", q.Has("a"), q.Len(), q.Ready())
	}
	it, ok := q.Peek()
	if !ok || it != (Item{ID: "a", Priority: 1, ReadyMs: 500, Payload: "pa"}) || q.Len() != 1 {
		t.Fatalf("peek %+v %v len=%d", it, ok, q.Len())
	}
	got, ok := q.Pop()
	if !ok || got != it || q.Len() != 0 || q.Has("a") {
		t.Fatalf("pop %+v %v", got, ok)
	}
	if _, ok := q.Pop(); ok {
		t.Fatal("pop from empty")
	}
	if _, ok := q.Peek(); ok {
		t.Fatal("peek from empty")
	}
	if err := q.Push("a", 0, 0, ""); err != nil {
		t.Fatalf("id free after pop: %v", err)
	}
}

func TestHiddenErrors(t *testing.T) {
	q := New(clock.NewFake(time.UnixMilli(0)))
	if err := q.Push("", 0, 0, ""); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("empty id: %v", err)
	}
	if err := q.Push("a", 0, -time.Millisecond, ""); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("negative delay: %v", err)
	}
	_ = q.Push("a", 0, 0, "")
	if err := q.Push("a", 9, time.Hour, "x"); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("dup: %v", err)
	}
	if it, _ := q.Peek(); it.Priority != 0 || q.Len() != 1 {
		t.Fatalf("refused push changed the queue: %+v", it)
	}
}

func TestHiddenOrdering(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(0))
	q := New(f)
	_ = q.Push("low-first", 1, 0, "")
	_ = q.Push("high-later", 5, 20*time.Millisecond, "")
	_ = q.Push("high-now-b", 5, 0, "")
	_ = q.Push("high-now-a", 5, 0, "")
	f.Advance(10 * time.Millisecond)
	_ = q.Push("high-early", 5, 0, "")
	f.Advance(10 * time.Millisecond)
	var got []string
	for {
		it, ok := q.Pop()
		if !ok {
			break
		}
		got = append(got, it.ID)
	}
	want := []string{"high-now-b", "high-now-a", "high-early", "high-later", "low-first"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v, want %v", got, want)
		}
	}
	_ = q.Push("neg", -1, 0, "")
	_ = q.Push("zero", 0, 0, "")
	if it, _ := q.Pop(); it.ID != "zero" {
		t.Fatalf("negative priority first: %s", it.ID)
	}
}

func TestHiddenDelay(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(1000))
	q := New(f)
	_ = q.Push("later", 9, 250*time.Millisecond, "")
	_ = q.Push("now", 0, 0, "")
	if q.Len() != 2 || q.Ready() != 1 {
		t.Fatalf("len=%d ready=%d", q.Len(), q.Ready())
	}
	if it, ok := q.Pop(); !ok || it.ID != "now" {
		t.Fatalf("%+v %v", it, ok)
	}
	f.Advance(249 * time.Millisecond)
	if _, ok := q.Peek(); ok || q.Ready() != 0 {
		t.Fatal("ready early")
	}
	f.Advance(time.Millisecond)
	it, ok := q.Pop()
	if !ok || it.ID != "later" || it.ReadyMs != 1250 || it.Priority != 9 {
		t.Fatalf("%+v %v", it, ok)
	}
	_ = q.Push("sub", 0, 999*time.Microsecond, "")
	if q.Ready() != 1 {
		t.Fatal("delay under a millisecond is a zero delay")
	}
}

func TestHiddenRemoveAndConcurrentPop(t *testing.T) {
	q := New(clock.NewFake(time.UnixMilli(0)))
	_ = q.Push("a", 0, 0, "")
	_ = q.Push("b", 0, time.Hour, "")
	if !q.Remove("b") || q.Remove("b") || q.Remove("zz") || q.Len() != 1 {
		t.Fatal("remove")
	}
	if !q.Remove("a") || q.Len() != 0 {
		t.Fatal("remove ready item")
	}
	for i := 0; i < 200; i++ {
		_ = q.Push("i"+string(rune('0'+i%10))+string(rune('0'+i/10%10))+string(rune('0'+i/100)), i%3, 0, "")
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	seen := map[string]bool{}
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				it, ok := q.Pop()
				if !ok {
					return
				}
				mu.Lock()
				if seen[it.ID] {
					t.Errorf("popped twice: %s", it.ID)
				}
				seen[it.ID] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 200 || q.Len() != 0 {
		t.Fatalf("%d popped, %d left", len(seen), q.Len())
	}
}
