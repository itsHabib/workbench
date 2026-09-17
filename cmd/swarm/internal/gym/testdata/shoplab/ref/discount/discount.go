package discount

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"shoplab/errs"
	"shoplab/money"
)

type Coupon struct {
	Code        string
	PercentBP   int64
	Fixed       money.Money
	MinSubtotal money.Money
	Expires     time.Time
	MaxUses     int
}

type entry struct {
	c    Coupon
	uses int
}

type Book struct {
	mu  sync.Mutex
	now func() time.Time
	m   map[string]*entry
}

func New(now func() time.Time) *Book { return &Book{now: now, m: map[string]*entry{}} }

func (b *Book) Add(c Coupon) error {
	c.Code = strings.ToUpper(c.Code)
	bad := c.Code == "" || (c.PercentBP != 0) == (c.Fixed != 0) ||
		c.PercentBP < 0 || c.PercentBP > 10000 || c.Fixed < 0 || c.MinSubtotal < 0 || c.MaxUses < 0
	if bad {
		return fmt.Errorf("discount: bad coupon %q: %w", c.Code, errs.ErrInvalid)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.m[c.Code]; ok {
		return fmt.Errorf("discount: coupon %q: %w", c.Code, errs.ErrConflict)
	}
	b.m[c.Code] = &entry{c: c}
	return nil
}

// quote does the checks. Callers hold the lock.
func (b *Book) quote(code string, subtotal money.Money) (*entry, money.Money, error) {
	e, ok := b.m[strings.ToUpper(code)]
	if !ok {
		return nil, 0, fmt.Errorf("discount: coupon %q: %w", code, errs.ErrNotFound)
	}
	if !e.c.Expires.IsZero() && !b.now().Before(e.c.Expires) {
		return nil, 0, fmt.Errorf("discount: coupon %q expired: %w", code, errs.ErrState)
	}
	if e.c.MaxUses > 0 && e.uses >= e.c.MaxUses {
		return nil, 0, fmt.Errorf("discount: coupon %q used up: %w", code, errs.ErrState)
	}
	if subtotal < 0 || subtotal < e.c.MinSubtotal {
		return nil, 0, fmt.Errorf("discount: coupon %q subtotal %s: %w", code, subtotal, errs.ErrInvalid)
	}
	if e.c.PercentBP != 0 {
		return e, subtotal.BP(e.c.PercentBP), nil
	}
	if e.c.Fixed > subtotal {
		return e, subtotal, nil
	}
	return e, e.c.Fixed, nil
}

func (b *Book) Quote(code string, subtotal money.Money) (money.Money, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, off, err := b.quote(code, subtotal)
	return off, err
}

func (b *Book) Redeem(code string, subtotal money.Money) (money.Money, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, off, err := b.quote(code, subtotal)
	if err != nil {
		return 0, err
	}
	e.uses++
	return off, nil
}

func (b *Book) Uses(code string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e, ok := b.m[strings.ToUpper(code)]; ok {
		return e.uses
	}
	return 0
}
