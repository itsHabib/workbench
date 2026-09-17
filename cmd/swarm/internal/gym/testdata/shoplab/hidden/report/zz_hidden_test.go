package report

import (
	"reflect"
	"testing"

	"shoplab/money"
	"shoplab/order"
	"shoplab/pricing"
	"shoplab/sku"
)

func ord(st order.State, total money.Money, lines ...order.Line) order.Order {
	return order.Order{State: st, Lines: lines, Quote: pricing.Quote{Total: total}}
}

func sample() []order.Order {
	return []order.Order{
		ord(order.Pending, 9999, order.Line{SKU: "TEA-001", Qty: 50}),
		ord(order.Paid, 1000, order.Line{SKU: "TEA-001", Qty: 2}, order.Line{SKU: "MUG-001", Qty: 1}),
		ord(order.Shipped, 2000, order.Line{SKU: "MUG-001", Qty: 1}, order.Line{SKU: "BOOK-01", Qty: 2}),
		ord(order.Delivered, 2001, order.Line{SKU: "ZZZ-001", Qty: 1}),
		ord(order.Refunded, 700, order.Line{SKU: "ZZZ-001", Qty: 9}),
		ord(order.Cancelled, 5555, order.Line{SKU: "ZZZ-001", Qty: 9}),
		ord(order.Paid, 0, order.Line{SKU: "AAA-001", Qty: 1}),
	}
}

func TestHiddenEmpty(t *testing.T) {
	s := Summarize(nil)
	if s.Orders != 0 || s.ByState == nil || s.Units == nil || len(s.ByState) != 0 || len(s.Units) != 0 || s.Gross != 0 || s.AOV != 0 {
		t.Fatalf("%+v", s)
	}
	if got := s.String(); got != "orders=0 gross=0.00 refunded=0.00 net=0.00 aov=0.00" {
		t.Fatalf("%q", got)
	}
	if got := s.Top(3); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestHiddenMoneyTotals(t *testing.T) {
	s := Summarize(sample())
	if s.Orders != 7 || s.Gross != 5701 || s.Refunded != 700 || s.Net != 5001 {
		t.Fatalf("%+v", s)
	}
	// 5001 over 4 kept orders is 1250.25.
	if s.AOV != 1250 {
		t.Fatalf("aov %d", s.AOV)
	}
	if got := s.String(); got != "orders=7 gross=57.01 refunded=7.00 net=50.01 aov=12.50" {
		t.Fatalf("%q", got)
	}
}

func TestHiddenAOVRounds(t *testing.T) {
	s := Summarize([]order.Order{ord(order.Paid, 100), ord(order.Paid, 101), ord(order.Refunded, 5000)})
	// 201 / 2 = 100.5, half away from zero.
	if s.AOV != 101 || s.Net != 201 {
		t.Fatalf("%+v", s)
	}
	only := Summarize([]order.Order{ord(order.Refunded, 5000), ord(order.Pending, 1)})
	if only.AOV != 0 || only.Gross != 5000 || only.Net != 0 {
		t.Fatalf("%+v", only)
	}
}

func TestHiddenByStateAndUnits(t *testing.T) {
	s := Summarize(sample())
	wantStates := map[order.State]int{order.Pending: 1, order.Paid: 2, order.Shipped: 1, order.Delivered: 1, order.Refunded: 1, order.Cancelled: 1}
	if !reflect.DeepEqual(s.ByState, wantStates) {
		t.Fatalf("%v", s.ByState)
	}
	wantUnits := map[sku.SKU]int{"TEA-001": 2, "MUG-001": 2, "BOOK-01": 2, "ZZZ-001": 1, "AAA-001": 1}
	if !reflect.DeepEqual(s.Units, wantUnits) {
		t.Fatalf("%v", s.Units)
	}
	few := Summarize(sample()[:2])
	if !reflect.DeepEqual(few.ByState, map[order.State]int{order.Pending: 1, order.Paid: 1}) {
		t.Fatalf("absent states must be absent: %v", few.ByState)
	}
}

func TestHiddenTop(t *testing.T) {
	s := Summarize(sample())
	all := []sku.SKU{"BOOK-01", "MUG-001", "TEA-001", "AAA-001", "ZZZ-001"}
	if got := s.Top(2); !reflect.DeepEqual(got, all[:2]) {
		t.Fatalf("top 2: %v", got)
	}
	if got := s.Top(4); !reflect.DeepEqual(got, all[:4]) {
		t.Fatalf("top 4: %v", got)
	}
	if got := s.Top(99); !reflect.DeepEqual(got, all) {
		t.Fatalf("top 99: %v", got)
	}
	for _, n := range []int{0, -1} {
		if got := s.Top(n); len(got) != 0 {
			t.Fatalf("top %d: %v", n, got)
		}
	}
}
