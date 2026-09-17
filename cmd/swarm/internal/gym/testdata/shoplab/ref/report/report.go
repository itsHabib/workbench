package report

import (
	"fmt"
	"sort"

	"shoplab/money"
	"shoplab/order"
	"shoplab/sku"
)

type Summary struct {
	Orders                    int
	ByState                   map[order.State]int
	Gross, Refunded, Net, AOV money.Money
	Units                     map[sku.SKU]int
}

func Summarize(orders []order.Order) Summary {
	s := Summary{Orders: len(orders), ByState: map[order.State]int{}, Units: map[sku.SKU]int{}}
	var kept int64
	for _, o := range orders {
		s.ByState[o.State]++
		switch o.State {
		case order.Refunded:
			s.Gross += o.Quote.Total
			s.Refunded += o.Quote.Total
		case order.Paid, order.Shipped, order.Delivered:
			s.Gross += o.Quote.Total
			kept++
			for _, l := range o.Lines {
				s.Units[l.SKU] += l.Qty
			}
		}
	}
	s.Net = s.Gross - s.Refunded
	if kept > 0 {
		s.AOV, _ = s.Net.Div(kept)
	}
	return s
}

func (s Summary) Top(n int) []sku.SKU {
	out := make([]sku.SKU, 0, len(s.Units))
	for k := range s.Units {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if s.Units[out[i]] != s.Units[out[j]] {
			return s.Units[out[i]] > s.Units[out[j]]
		}
		return out[i] < out[j]
	})
	if n < 0 {
		n = 0
	}
	if n < len(out) {
		out = out[:n]
	}
	return out
}

func (s Summary) String() string {
	return fmt.Sprintf("orders=%d gross=%s refunded=%s net=%s aov=%s", s.Orders, s.Gross, s.Refunded, s.Net, s.AOV)
}
