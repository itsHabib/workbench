package lease

import (
	"fmt"
	"sync"
	"time"

	"schedlab/clock"
	"schedlab/errs"
)

type Lease struct {
	Key, Holder string
	Epoch       int64
	ExpiresMs   int64
}

type Store struct {
	mu     sync.Mutex
	c      clock.Clock
	leases map[string]Lease
	epochs map[string]int64
}

func New(c clock.Clock) *Store {
	return &Store{c: c, leases: map[string]Lease{}, epochs: map[string]int64{}}
}

func (s *Store) live(key string) (Lease, bool) {
	l, ok := s.leases[key]
	if !ok || clock.Millis(s.c) >= l.ExpiresMs {
		return Lease{}, false
	}
	return l, true
}

func (s *Store) Acquire(key, holder string, ttl time.Duration) (Lease, error) {
	if key == "" || holder == "" || ttl < time.Millisecond {
		return Lease{}, fmt.Errorf("lease: key %q holder %q ttl %v: %w", key, holder, ttl, errs.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	expires := clock.Millis(s.c) + ttl.Milliseconds()
	cur, ok := s.live(key)
	if ok && cur.Holder != holder {
		return Lease{}, fmt.Errorf("lease: %q held by %q: %w", key, cur.Holder, errs.ErrConflict)
	}
	if ok {
		cur.ExpiresMs = expires
		s.leases[key] = cur
		return cur, nil
	}
	s.epochs[key]++
	l := Lease{Key: key, Holder: holder, Epoch: s.epochs[key], ExpiresMs: expires}
	s.leases[key] = l
	return l, nil
}

func (s *Store) check(key, holder string, epoch int64) error {
	cur, ok := s.live(key)
	if !ok {
		return fmt.Errorf("lease: %q: %w", key, errs.ErrExpired)
	}
	if cur.Holder != holder || cur.Epoch != epoch {
		return fmt.Errorf("lease: %q held by %q epoch %d: %w", key, cur.Holder, cur.Epoch, errs.ErrState)
	}
	return nil
}

func (s *Store) Check(key, holder string, epoch int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.check(key, holder, epoch)
}

func (s *Store) Release(key, holder string, epoch int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(key, holder, epoch); err != nil {
		return err
	}
	delete(s.leases, key)
	return nil
}

func (s *Store) Get(key string) (Lease, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live(key)
}

func (s *Store) Epoch(key string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.epochs[key]
}
