package queue

import (
	"fmt"
	"sync"
	"time"

	"schedlab/clock"
	"schedlab/errs"
)

type Item struct {
	ID       string
	Priority int
	ReadyMs  int64
	Payload  string
}

type entry struct {
	item Item
	seq  int64
}

type Queue struct {
	mu    sync.Mutex
	c     clock.Clock
	items []entry
	seq   int64
}

func New(c clock.Clock) *Queue { return &Queue{c: c} }

func (q *Queue) index(id string) int {
	for i, e := range q.items {
		if e.item.ID == id {
			return i
		}
	}
	return -1
}

func (q *Queue) Push(id string, priority int, delay time.Duration, payload string) error {
	if id == "" || delay < 0 {
		return fmt.Errorf("queue: id %q delay %v: %w", id, delay, errs.ErrInvalid)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.index(id) >= 0 {
		return fmt.Errorf("queue: %q queued: %w", id, errs.ErrConflict)
	}
	q.seq++
	q.items = append(q.items, entry{
		item: Item{ID: id, Priority: priority, ReadyMs: clock.Millis(q.c) + delay.Milliseconds(), Payload: payload},
		seq:  q.seq,
	})
	return nil
}

func better(a, b entry) bool {
	if a.item.Priority != b.item.Priority {
		return a.item.Priority > b.item.Priority
	}
	if a.item.ReadyMs != b.item.ReadyMs {
		return a.item.ReadyMs < b.item.ReadyMs
	}
	return a.seq < b.seq
}

func (q *Queue) best() int {
	now := clock.Millis(q.c)
	best := -1
	for i, e := range q.items {
		if e.item.ReadyMs > now {
			continue
		}
		if best < 0 || better(e, q.items[best]) {
			best = i
		}
	}
	return best
}

func (q *Queue) Pop() (Item, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := q.best()
	if i < 0 {
		return Item{}, false
	}
	it := q.items[i].item
	q.items = append(q.items[:i], q.items[i+1:]...)
	return it, true
}

func (q *Queue) Peek() (Item, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := q.best()
	if i < 0 {
		return Item{}, false
	}
	return q.items[i].item, true
}

func (q *Queue) Remove(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := q.index(id)
	if i < 0 {
		return false
	}
	q.items = append(q.items[:i], q.items[i+1:]...)
	return true
}

func (q *Queue) Has(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.index(id) >= 0
}

func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *Queue) Ready() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	now := clock.Millis(q.c)
	n := 0
	for _, e := range q.items {
		if e.item.ReadyMs <= now {
			n++
		}
	}
	return n
}
