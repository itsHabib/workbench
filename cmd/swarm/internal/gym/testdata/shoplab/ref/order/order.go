package order

import (
	"fmt"
	"sync"
	"time"

	"shoplab/errs"
	"shoplab/money"
	"shoplab/pricing"
	"shoplab/sku"
)

type State string

const (
	Pending   State = "PENDING"
	Paid      State = "PAID"
	Shipped   State = "SHIPPED"
	Delivered State = "DELIVERED"
	Cancelled State = "CANCELLED"
	Refunded  State = "REFUNDED"
)

var moves = map[State][]State{
	Pending: {Paid, Cancelled},
	Paid:    {Shipped, Refunded},
	Shipped: {Delivered},
}

func CanTransition(from, to State) bool {
	for _, s := range moves[from] {
		if s == to {
			return true
		}
	}
	return false
}

type Line struct {
	SKU   sku.SKU
	Qty   int
	Total money.Money
}

type Order struct {
	ID                   string
	State                State
	Lines                []Line
	Quote                pricing.Quote
	CreatedMs, UpdatedMs int64
}

type Book struct {
	mu     sync.Mutex
	now    func() time.Time
	orders []*Order
	byID   map[string]*Order
}

func NewBook(now func() time.Time) *Book { return &Book{now: now, byID: map[string]*Order{}} }

func (o *Order) copy() Order {
	cp := *o
	cp.Lines = append([]Line(nil), o.Lines...)
	return cp
}

func (b *Book) Create(lines []Line, q pricing.Quote) (Order, error) {
	if len(lines) == 0 || q.Total < 0 {
		return Order{}, fmt.Errorf("order: %d lines, total %s: %w", len(lines), q.Total, errs.ErrInvalid)
	}
	for _, l := range lines {
		if l.Qty <= 0 {
			return Order{}, fmt.Errorf("order: %s qty %d: %w", l.SKU, l.Qty, errs.ErrInvalid)
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	ms := b.now().UnixMilli()
	o := &Order{ID: fmt.Sprintf("O-%d", len(b.orders)+1), State: Pending, Lines: append([]Line(nil), lines...),
		Quote: q, CreatedMs: ms, UpdatedMs: ms}
	b.orders = append(b.orders, o)
	b.byID[o.ID] = o
	return o.copy(), nil
}

func (b *Book) Get(id string) (Order, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	o, ok := b.byID[id]
	if !ok {
		return Order{}, fmt.Errorf("order: %q: %w", id, errs.ErrNotFound)
	}
	return o.copy(), nil
}

func (b *Book) List() []Order {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Order, len(b.orders))
	for i, o := range b.orders {
		out[i] = o.copy()
	}
	return out
}

// move applies the first allowed target state. check may veto it.
func (b *Book) move(id string, check func(*Order) error, targets ...State) (State, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	o, ok := b.byID[id]
	if !ok {
		return "", fmt.Errorf("order: %q: %w", id, errs.ErrNotFound)
	}
	for _, to := range targets {
		if !CanTransition(o.State, to) {
			continue
		}
		if check != nil {
			if err := check(o); err != nil {
				return "", err
			}
		}
		o.State = to
		o.UpdatedMs = b.now().UnixMilli()
		return to, nil
	}
	return "", fmt.Errorf("order: %s is %s: %w", id, o.State, errs.ErrState)
}

func (b *Book) Pay(id string, amount money.Money) error {
	_, err := b.move(id, func(o *Order) error {
		if amount != o.Quote.Total {
			return fmt.Errorf("order: %s costs %s, got %s: %w", o.ID, o.Quote.Total, amount, errs.ErrInvalid)
		}
		return nil
	}, Paid)
	return err
}

func (b *Book) Ship(id string) error {
	_, err := b.move(id, nil, Shipped)
	return err
}

func (b *Book) Deliver(id string) error {
	_, err := b.move(id, nil, Delivered)
	return err
}

func (b *Book) Cancel(id string) (State, error) { return b.move(id, nil, Cancelled, Refunded) }
