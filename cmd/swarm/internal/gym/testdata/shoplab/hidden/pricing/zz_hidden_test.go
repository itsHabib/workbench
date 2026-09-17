package pricing

import (
	"errors"
	"testing"

	"shoplab/cart"
	"shoplab/errs"
	"shoplab/shipping"
)

func calc(t *testing.T) *Calc {
	t.Helper()
	c, err := New(map[string]int64{"general": 1000, "food": 500, "book": 0})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func two() []cart.Line {
	return []cart.Line{
		{SKU: "MUG-001", Qty: 1, Unit: 1000, Total: 1000, WeightG: 600, Category: "general"},
		{SKU: "TEA-001", Qty: 1, Unit: 505, Total: 505, WeightG: 600, Category: "food"},
	}
}

func TestHiddenSpecExample(t *testing.T) {
	got, err := calc(t).Quote(two(), 150, shipping.Domestic)
	want := Quote{Subtotal: 1505, Discount: 150, Tax: 113, Shipping: 650, Total: 2118}
	if err != nil || got != want {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestHiddenTaxIsPerLine(t *testing.T) {
	// Three lines of 0.05 at 10%: each rounds up to 0.01, where 10% of the sum would round to 0.02.
	lines := []cart.Line{
		{SKU: "AAA", Qty: 1, Total: 5, Category: "general"},
		{SKU: "BBB", Qty: 1, Total: 5, Category: "general"},
		{SKU: "CCC", Qty: 1, Total: 5, Category: "general"},
	}
	got, err := calc(t).Quote(lines, 0, shipping.Domestic)
	if err != nil || got.Tax != 3 || got.Shipping != 0 || got.Total != 18 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestHiddenUntaxedAndInternational(t *testing.T) {
	lines := append(two(),
		cart.Line{SKU: "BOOK-01", Qty: 1, Total: 1999, WeightG: 100, Category: "book"},
		cart.Line{SKU: "GIFT-01", Qty: 1, Total: 1000, WeightG: 0, Category: "unheard-of"})
	got, err := calc(t).Quote(lines, 0, shipping.Domestic)
	if err != nil || got.Tax != 100+25 || got.Subtotal != 4504 || got.Shipping != 650 || got.Total != 4504+125+650 {
		t.Fatalf("domestic %+v %v", got, err)
	}
	got, err = calc(t).Quote(lines, 0, shipping.International)
	if err != nil || got.Tax != 0 || got.Shipping != 1900 || got.Total != 4504+1900 {
		t.Fatalf("international %+v %v", got, err)
	}
}

func TestHiddenFreeShippingUsesDiscountedSubtotal(t *testing.T) {
	lines := []cart.Line{{SKU: "BOOK-01", Qty: 3, Total: 5100, WeightG: 1800, Category: "book"}}
	got, err := calc(t).Quote(lines, 0, shipping.Domestic)
	if err != nil || got.Shipping != 0 || got.Total != 5100 {
		t.Fatalf("no discount %+v %v", got, err)
	}
	got, err = calc(t).Quote(lines, 101, shipping.Domestic)
	if err != nil || got.Shipping != 800 || got.Total != 5100-101+800 {
		t.Fatalf("discounted below 50.00 %+v %v", got, err)
	}
	got, err = calc(t).Quote(lines, 5100, shipping.Domestic)
	if err != nil || got.Total != 800 || got.Tax != 0 {
		t.Fatalf("fully discounted %+v %v", got, err)
	}
}

func TestHiddenRejects(t *testing.T) {
	if _, err := New(map[string]int64{"x": 10001}); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("rate too big: %v", err)
	}
	if _, err := New(map[string]int64{"x": -1}); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("negative rate: %v", err)
	}
	c := calc(t)
	if _, err := c.Quote(nil, 0, shipping.Domestic); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("no lines: %v", err)
	}
	if _, err := c.Quote(two(), -1, shipping.Domestic); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("negative discount: %v", err)
	}
	if _, err := c.Quote(two(), 1506, shipping.Domestic); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("discount over subtotal: %v", err)
	}
	if _, err := c.Quote(two(), 0, "EU"); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("zone: %v", err)
	}
	heavy := []cart.Line{{SKU: "ANVIL-1", Qty: 1, Total: 100, WeightG: 30001, Category: "general"}}
	if _, err := c.Quote(heavy, 0, shipping.Domestic); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("too heavy: %v", err)
	}
}

func TestHiddenRatesAreCopied(t *testing.T) {
	rates := map[string]int64{"general": 1000}
	c, err := New(rates)
	if err != nil {
		t.Fatal(err)
	}
	rates["general"] = 0
	got, err := c.Quote([]cart.Line{{SKU: "MUG-001", Qty: 1, Total: 1000, Category: "general"}}, 0, shipping.Domestic)
	if err != nil || got.Tax != 100 {
		t.Fatalf("%+v %v", got, err)
	}
}
