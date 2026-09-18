package clock

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

type system struct{}

func (system) Now() time.Time { return time.Now() }

func System() Clock { return system{} }

func Millis(c Clock) int64 { return c.Now().UnixMilli() }

type Fake struct {
	mu  sync.Mutex
	now time.Time
}

func NewFake(start time.Time) *Fake { return &Fake{now: start} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) Advance(d time.Duration) time.Time {
	if d < 0 {
		panic("clock: negative advance")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	return f.now
}

func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t
}
