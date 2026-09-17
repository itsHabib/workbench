package store

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func TestHiddenSetGet(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := New(c.now)
	s.Set("a", "1", 0)
	if v, ok := s.Get("a"); !ok || v != "1" {
		t.Fatalf("got %q %v", v, ok)
	}
	if _, ok := s.Get("b"); ok {
		t.Fatal("b present")
	}
	c.add(1000 * time.Hour)
	if _, ok := s.Get("a"); !ok {
		t.Fatal("ttl 0 expired")
	}
}

func TestHiddenExpiryBoundary(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := New(c.now)
	s.Set("a", "1", 10*time.Second)
	c.add(10*time.Second - time.Nanosecond)
	if _, ok := s.Get("a"); !ok {
		t.Fatal("expired early")
	}
	c.add(time.Nanosecond)
	if _, ok := s.Get("a"); ok {
		t.Fatal("alive at the boundary")
	}
	if s.Len() != 0 || len(s.Keys()) != 0 {
		t.Fatal("expired key counted")
	}
}

func TestHiddenResetReplacesTTL(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := New(c.now)
	s.Set("a", "1", time.Second)
	s.Set("a", "2", 0)
	c.add(time.Hour)
	if v, ok := s.Get("a"); !ok || v != "2" {
		t.Fatalf("got %q %v", v, ok)
	}
}

func TestHiddenDelete(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := New(c.now)
	s.Set("a", "1", time.Second)
	s.Set("b", "1", 0)
	if !s.Delete("b") || s.Delete("b") || s.Delete("nope") {
		t.Fatal("delete of a live or missing key")
	}
	c.add(time.Minute)
	if s.Delete("a") {
		t.Fatal("deleted an expired key")
	}
}

func TestHiddenKeysSortedAndConcurrent(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	s := New(c.now)
	var wg sync.WaitGroup
	for _, k := range []string{"d", "b", "a", "c"} {
		wg.Add(1)
		go func(k string) { defer wg.Done(); s.Set(k, k, 0); s.Get(k); s.Keys() }(k)
	}
	wg.Wait()
	if got := s.Keys(); !reflect.DeepEqual(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("keys %v", got)
	}
	if s.Len() != 4 {
		t.Fatal("len")
	}
}
