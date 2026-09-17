package pricing

import (
	"fmt"

	"shoplab/cart"
	"shoplab/errs"
	"shoplab/money"
	"shoplab/shipping"
)

type Quote struct {
	Subtotal, Discount, Tax, Shipping, Total money.Money
}

type Calc struct{ taxBP map[string]int64 }

func New(taxBP map[string]int64) (*Calc, error) {
	cp := make(map[string]int64, len(taxBP))
	for cat, bp := range taxBP {
		if bp < 0 || bp > 10000 {
			return nil, fmt.Errorf("pricing: rate %d for %q: %w", bp, cat, errs.ErrInvalid)
		}
		cp[cat] = bp
	}
	return &Calc{taxBP: cp}, nil
}

func (c *Calc) Quote(lines []cart.Line, discount money.Money, zone shipping.Zone) (Quote, error) {
	q := Quote{Discount: discount}
	weights := make([]int64, len(lines))
	grams := 0
	for i, l := range lines {
		q.Subtotal += l.Total
		weights[i] = int64(l.Total)
		grams += l.WeightG
	}
	if len(lines) == 0 || discount < 0 || discount > q.Subtotal {
		return Quote{}, fmt.Errorf("pricing: %d lines, discount %s: %w", len(lines), discount, errs.ErrInvalid)
	}
	shares := make([]money.Money, len(lines))
	if discount != 0 {
		var err error
		if shares, err = money.Allocate(discount, weights); err != nil {
			return Quote{}, err
		}
	}
	if zone != shipping.International {
		for i, l := range lines {
			q.Tax += (l.Total - shares[i]).BP(c.taxBP[l.Category])
		}
	}
	ship, err := shipping.Rate(zone, grams, q.Subtotal-discount)
	if err != nil {
		return Quote{}, err
	}
	q.Shipping = ship
	q.Total = q.Subtotal - q.Discount + q.Tax + q.Shipping
	return q, nil
}
