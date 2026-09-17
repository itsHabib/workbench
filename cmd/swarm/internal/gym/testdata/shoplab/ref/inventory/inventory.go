package inventory

import (
	"fmt"
	"sync"
	"time"

	"shoplab/errs"
	"shoplab/sku"
)

type reservation struct {
	items map[sku.SKU]int
	at    time.Time
	ttl   time.Duration
}

type Inventory struct {
	mu     sync.Mutex
	now    func() time.Time
	onHand map[sku.SKU]int
	res    map[string]reservation
}

func New(now func() time.Time) *Inventory {
	return &Inventory{now: now, onHand: map[sku.SKU]int{}, res: map[string]reservation{}}
}

// purge drops expired reservations. Callers hold the lock.
func (v *Inventory) purge() {
	now := v.now()
	for id, r := range v.res {
		if r.ttl != 0 && !now.Before(r.at.Add(r.ttl)) {
			delete(v.res, id)
		}
	}
}

func (v *Inventory) available(s sku.SKU) int {
	n := v.onHand[s]
	for _, r := range v.res {
		n -= r.items[s]
	}
	return n
}

func (v *Inventory) Receive(s sku.SKU, qty int) error {
	if qty <= 0 {
		return fmt.Errorf("inventory: receive %d: %w", qty, errs.ErrInvalid)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.onHand[s] += qty
	return nil
}

func (v *Inventory) OnHand(s sku.SKU) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.onHand[s]
}

func (v *Inventory) Available(s sku.SKU) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.purge()
	return v.available(s)
}

func (v *Inventory) Reserve(id string, items map[sku.SKU]int, ttl time.Duration) error {
	if id == "" || len(items) == 0 {
		return fmt.Errorf("inventory: empty reservation: %w", errs.ErrInvalid)
	}
	for s, q := range items {
		if q <= 0 {
			return fmt.Errorf("inventory: %s qty %d: %w", s, q, errs.ErrInvalid)
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.purge()
	if _, ok := v.res[id]; ok {
		return fmt.Errorf("inventory: reservation %q: %w", id, errs.ErrConflict)
	}
	cp := make(map[sku.SKU]int, len(items))
	for s, q := range items {
		if q > v.available(s) {
			return fmt.Errorf("inventory: %s: %w", s, errs.ErrInsufficient)
		}
		cp[s] = q
	}
	v.res[id] = reservation{items: cp, at: v.now(), ttl: ttl}
	return nil
}

func (v *Inventory) take(id string) (reservation, error) {
	v.purge()
	r, ok := v.res[id]
	if !ok {
		return r, fmt.Errorf("inventory: reservation %q: %w", id, errs.ErrNotFound)
	}
	delete(v.res, id)
	return r, nil
}

func (v *Inventory) Release(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, err := v.take(id)
	return err
}

func (v *Inventory) Commit(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	r, err := v.take(id)
	if err != nil {
		return err
	}
	for s, q := range r.items {
		v.onHand[s] -= q
	}
	return nil
}

func (v *Inventory) Reserved(id string) (map[sku.SKU]int, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.purge()
	r, ok := v.res[id]
	if !ok {
		return nil, false
	}
	cp := make(map[sku.SKU]int, len(r.items))
	for s, q := range r.items {
		cp[s] = q
	}
	return cp, true
}
