package cart

import (
	"fmt"
	"sort"

	"shoplab/catalog"
	"shoplab/errs"
	"shoplab/money"
	"shoplab/sku"
)

const maxQty = 999

type Line struct {
	SKU         sku.SKU
	Name        string
	Qty         int
	Unit, Total money.Money
	WeightG     int
	Category    string
}

type Cart struct {
	id    string
	cat   *catalog.Catalog
	items map[sku.SKU]int
}

func New(id string, cat *catalog.Catalog) *Cart {
	return &Cart{id: id, cat: cat, items: map[sku.SKU]int{}}
}

func (c *Cart) ID() string { return c.id }

func (c *Cart) Add(s sku.SKU, qty int) error {
	if qty <= 0 {
		return fmt.Errorf("cart: add %d: %w", qty, errs.ErrInvalid)
	}
	return c.Set(s, c.items[s]+qty)
}

func (c *Cart) Set(s sku.SKU, qty int) error {
	if qty < 0 || qty > maxQty {
		return fmt.Errorf("cart: qty %d: %w", qty, errs.ErrInvalid)
	}
	if _, err := c.cat.Get(s); err != nil {
		return err
	}
	if qty == 0 {
		delete(c.items, s)
		return nil
	}
	c.items[s] = qty
	return nil
}

func (c *Cart) Remove(s sku.SKU) error {
	if _, ok := c.items[s]; !ok {
		return fmt.Errorf("cart: no line %s: %w", s, errs.ErrNotFound)
	}
	delete(c.items, s)
	return nil
}

func (c *Cart) Clear() { c.items = map[sku.SKU]int{} }

func (c *Cart) Lines() ([]Line, error) {
	out := make([]Line, 0, len(c.items))
	for s, q := range c.items {
		p, err := c.cat.Get(s)
		if err != nil {
			return nil, err
		}
		out = append(out, Line{SKU: s, Name: p.Name, Qty: q, Unit: p.Price, Total: p.Price.Mul(q),
			WeightG: p.WeightG * q, Category: p.Category})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SKU < out[j].SKU })
	return out, nil
}

func (c *Cart) Subtotal() (money.Money, error) {
	lines, err := c.Lines()
	var t money.Money
	for _, l := range lines {
		t += l.Total
	}
	return t, err
}

func (c *Cart) WeightG() (int, error) {
	lines, err := c.Lines()
	g := 0
	for _, l := range lines {
		g += l.WeightG
	}
	return g, err
}

func (c *Cart) Items() map[sku.SKU]int {
	out := make(map[sku.SKU]int, len(c.items))
	for s, q := range c.items {
		out[s] = q
	}
	return out
}

func (c *Cart) Len() int { return len(c.items) }
