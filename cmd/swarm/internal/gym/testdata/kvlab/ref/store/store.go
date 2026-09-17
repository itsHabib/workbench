package store

import (
	"sort"
	"sync"
	"time"
)

type item struct {
	val string
	exp time.Time
}

type Store struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]item
}

func New(now func() time.Time) *Store { return &Store{now: now, m: map[string]item{}} }

func (s *Store) live(k string) (item, bool) {
	it, ok := s.m[k]
	if !ok {
		return it, false
	}
	if !it.exp.IsZero() && !s.now().Before(it.exp) {
		delete(s.m, k)
		return it, false
	}
	return it, true
}

func (s *Store) Set(key, val string, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it := item{val: val}
	if ttl > 0 {
		it.exp = s.now().Add(ttl)
	}
	s.m[key] = it
}

func (s *Store) Get(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.live(key)
	return it.val, ok
}

func (s *Store) Delete(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.live(key)
	delete(s.m, key)
	return ok
}

func (s *Store) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []string{}
	for k := range s.m {
		if _, ok := s.live(k); ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Store) Len() int { return len(s.Keys()) }
