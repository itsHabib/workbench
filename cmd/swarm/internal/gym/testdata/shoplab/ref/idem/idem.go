package idem

import (
	"sync"
	"time"
)

type record struct {
	reply string
	at    time.Time
}

type Store struct {
	mu  sync.Mutex
	now func() time.Time
	ttl time.Duration
	m   map[string]record
}

func New(now func() time.Time, ttl time.Duration) *Store {
	return &Store{now: now, ttl: ttl, m: map[string]record{}}
}

func (s *Store) live(r record) bool {
	return s.ttl == 0 || s.now().Before(r.at.Add(s.ttl))
}

func (s *Store) Do(key string, fn func() (string, error)) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		reply, err := fn()
		return reply, false, err
	}
	if r, ok := s.m[key]; ok && s.live(r) {
		return r.reply, true, nil
	}
	delete(s.m, key)
	reply, err := fn()
	if err != nil {
		return reply, false, err
	}
	s.m[key] = record{reply: reply, at: s.now()}
	return reply, false, nil
}

func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for k, r := range s.m {
		if !s.live(r) {
			delete(s.m, k)
			continue
		}
		n++
	}
	return n
}
