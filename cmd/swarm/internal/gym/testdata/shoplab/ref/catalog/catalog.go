package catalog

import (
	"fmt"
	"sort"
	"sync"

	"shoplab/errs"
	"shoplab/money"
	"shoplab/sku"
)

type Product struct {
	SKU      sku.SKU
	Name     string
	Price    money.Money
	WeightG  int
	Category string
}

type Catalog struct {
	mu sync.RWMutex
	m  map[sku.SKU]Product
}

func New() *Catalog { return &Catalog{m: map[sku.SKU]Product{}} }

func (c *Catalog) Put(p Product) error {
	canon, err := sku.Parse(string(p.SKU))
	if err != nil {
		return err
	}
	if canon != p.SKU || p.Name == "" || p.Category == "" || p.Price < 0 || p.WeightG < 0 {
		return fmt.Errorf("catalog: bad product %q: %w", p.SKU, errs.ErrInvalid)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[p.SKU] = p
	return nil
}

func (c *Catalog) Get(s sku.SKU) (Product, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok := c.m[s]
	if !ok {
		return Product{}, fmt.Errorf("catalog: %s: %w", s, errs.ErrNotFound)
	}
	return p, nil
}

func (c *Catalog) Remove(s sku.SKU) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.m[s]; !ok {
		return fmt.Errorf("catalog: %s: %w", s, errs.ErrNotFound)
	}
	delete(c.m, s)
	return nil
}

func (c *Catalog) List() []Product {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Product, 0, len(c.m))
	for _, p := range c.m {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SKU < out[j].SKU })
	return out
}

func (c *Catalog) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.m)
}
