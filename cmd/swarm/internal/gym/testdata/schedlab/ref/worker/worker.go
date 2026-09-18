package worker

import (
	"fmt"
	"time"

	"schedlab/clock"
	"schedlab/errs"
	"schedlab/lease"
	"schedlab/queue"
)

type Result struct {
	ItemID, Worker string
	Epoch          int64
	OK             bool
	Output, Msg    string
}

type Handler func(item queue.Item) (string, error)

type Pool struct {
	c   clock.Clock
	q   *queue.Queue
	ls  *lease.Store
	ttl time.Duration
	h   Handler
}

func New(c clock.Clock, q *queue.Queue, ls *lease.Store, ttl time.Duration, h Handler) (*Pool, error) {
	if q == nil || ls == nil || h == nil || ttl < time.Millisecond {
		return nil, fmt.Errorf("worker: bad pool: %w", errs.ErrInvalid)
	}
	return &Pool{c: c, q: q, ls: ls, ttl: ttl, h: h}, nil
}

func (p *Pool) Run(name string) (Result, bool, error) {
	if name == "" {
		return Result{}, false, fmt.Errorf("worker: empty name: %w", errs.ErrInvalid)
	}
	item, ok := p.q.Pop()
	if !ok {
		return Result{}, false, nil
	}
	l, err := p.ls.Acquire(item.ID, name, p.ttl)
	if err != nil {
		_ = p.q.Push(item.ID, item.Priority, 0, item.Payload)
		return Result{}, false, err
	}
	out, herr := p.h(item)
	r := Result{ItemID: item.ID, Worker: name, Epoch: l.Epoch, OK: herr == nil, Output: out}
	if herr != nil {
		r.Msg = herr.Error()
	}
	return r, true, nil
}

func (p *Pool) Drain(name string, max int) ([]Result, error) {
	var out []Result
	for max <= 0 || len(out) < max {
		r, ok, err := p.Run(name)
		if err != nil {
			return out, err
		}
		if !ok {
			break
		}
		out = append(out, r)
	}
	return out, nil
}
