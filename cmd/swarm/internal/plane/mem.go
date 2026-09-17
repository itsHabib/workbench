package plane

import (
	"context"
	"sync"
	"time"
)

// Mem is the in-memory reference store: the transitions under one mutex.
// It is what the conformance suite and the model tests run against, and,
// with a Broken set, what proves the checker can fail.
type Mem struct {
	mu     sync.Mutex
	d      *Data
	Broken Broken
	Now    func() time.Time
}

// NewMem returns an empty in-memory store.
func NewMem() *Mem { return &Mem{d: NewData(), Now: func() time.Time { return time.Now().UTC() }} }

func (m *Mem) Put(_ context.Context, it Item) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.d.put(m.Now(), it)
	return nil
}

func (m *Mem) Claim(_ context.Context, kind, id, inc string, ttl time.Duration, idem string) (Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.d.claim(m.Now(), kind, id, inc, ttl, idem, m.Broken)
}

func (m *Mem) ClaimNext(_ context.Context, kind, inc string, ttl time.Duration, idem string) (Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.d.claimNext(m.Now(), kind, inc, ttl, idem, m.Broken)
}

func (m *Mem) Renew(_ context.Context, g Grant, ttl time.Duration) (Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.d.renew(m.Now(), g, ttl)
}

func (m *Mem) Release(_ context.Context, g Grant) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.d.release(m.Now(), g, m.Broken)
}

func (m *Mem) Commit(_ context.Context, g Grant, result, idem string, emit []Item) (Receipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.d.commit(m.Now(), g, result, idem, emit, m.Broken)
}

func (m *Mem) Get(_ context.Context, kind, id string) (Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.d.Items[key(kind, id)]
	if !ok {
		return Item{}, ErrNotFound
	}
	return *it, nil
}

func (m *Mem) List(_ context.Context, kind string) ([]Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.d.list(kind), nil
}

func (m *Mem) History(context.Context) ([]Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Event(nil), m.d.Log...), nil
}

func (m *Mem) Close() error { return nil }
