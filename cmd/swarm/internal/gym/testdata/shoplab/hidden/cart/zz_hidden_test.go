package cart

import (
	"errors"
	"reflect"
	"testing"

	"shoplab/catalog"
	"shoplab/errs"
	"shoplab/sku"
)

func shelf(t *testing.T) *catalog.Catalog {
	t.Helper()
	c := catalog.New()
	for _, p := range []catalog.Product{
		{SKU: "TEA-001", Name: "Green Tea", Price: 505, WeightG: 120, Category: "food"},
		{SKU: "MUG-001", Name: "Mug", Price: 1000, WeightG: 350, Category: "general"},
		{SKU: "BOOK-01", Name: "Tea Book", Price: 1999, WeightG: 600, Category: "book"},
	} {
		if err := c.Put(p); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func TestHiddenAddAndLines(t *testing.T) {
	c := New("c1", shelf(t))
	if c.ID() != "c1" || c.Len() != 0 {
		t.Fatalf("%q %d", c.ID(), c.Len())
	}
	for _, step := range []struct {
		s sku.SKU
		q int
	}{{"TEA-001", 2}, {"MUG-001", 1}, {"TEA-001", 1}} {
		if err := c.Add(step.s, step.q); err != nil {
			t.Fatal(err)
		}
	}
	lines, err := c.Lines()
	want := []Line{
		{SKU: "MUG-001", Name: "Mug", Qty: 1, Unit: 1000, Total: 1000, WeightG: 350, Category: "general"},
		{SKU: "TEA-001", Name: "Green Tea", Qty: 3, Unit: 505, Total: 1515, WeightG: 360, Category: "food"},
	}
	if err != nil || !reflect.DeepEqual(lines, want) {
		t.Fatalf("%+v %v", lines, err)
	}
	sub, _ := c.Subtotal()
	grams, _ := c.WeightG()
	if sub != 2515 || grams != 710 || c.Len() != 2 {
		t.Fatalf("subtotal %d grams %d len %d", sub, grams, c.Len())
	}
}

func TestHiddenAddRejects(t *testing.T) {
	c := New("c1", shelf(t))
	for _, q := range []int{0, -1} {
		if err := c.Add("TEA-001", q); !errors.Is(err, errs.ErrInvalid) {
			t.Fatalf("qty %d: %v", q, err)
		}
	}
	if err := c.Add("NOPE-1", 1); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("unknown sku: %v", err)
	}
	if err := c.Add("TEA-001", 999); err != nil {
		t.Fatal(err)
	}
	if err := c.Add("TEA-001", 1); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("over 999: %v", err)
	}
	if err := c.Add("MUG-001", 1000); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("1000 at once: %v", err)
	}
	if got := c.Items(); len(got) != 1 || got["TEA-001"] != 999 {
		t.Fatalf("%v", got)
	}
}

func TestHiddenSetRemoveClear(t *testing.T) {
	c := New("c1", shelf(t))
	if err := c.Set("TEA-001", 5); err != nil {
		t.Fatal(err)
	}
	if err := c.Set("TEA-001", 2); err != nil || c.Items()["TEA-001"] != 2 {
		t.Fatalf("set replaces: %v %v", err, c.Items())
	}
	if err := c.Set("MUG-001", 0); err != nil || c.Len() != 1 {
		t.Fatalf("set 0 of an absent line: %v", err)
	}
	for _, q := range []int{-1, 1000} {
		if err := c.Set("TEA-001", q); !errors.Is(err, errs.ErrInvalid) {
			t.Fatalf("set %d: %v", q, err)
		}
	}
	if err := c.Set("NOPE-1", 1); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("set unknown: %v", err)
	}
	if err := c.Set("TEA-001", 0); err != nil || c.Len() != 0 {
		t.Fatalf("set 0: %v len=%d", err, c.Len())
	}
	if err := c.Remove("TEA-001"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("remove absent: %v", err)
	}
	_ = c.Add("TEA-001", 1)
	_ = c.Add("MUG-001", 1)
	if err := c.Remove("TEA-001"); err != nil || c.Len() != 1 {
		t.Fatalf("remove: %v", err)
	}
	c.Clear()
	if lines, err := c.Lines(); err != nil || len(lines) != 0 || c.Len() != 0 {
		t.Fatalf("after clear: %v %v", lines, err)
	}
	if sub, err := c.Subtotal(); err != nil || sub != 0 {
		t.Fatalf("empty subtotal: %d %v", sub, err)
	}
}

func TestHiddenPricesAreReadLate(t *testing.T) {
	cat := shelf(t)
	c := New("c1", cat)
	_ = c.Add("TEA-001", 2)
	_ = cat.Put(catalog.Product{SKU: "TEA-001", Name: "Greener Tea", Price: 600, WeightG: 100, Category: "food"})
	lines, err := c.Lines()
	if err != nil || lines[0].Name != "Greener Tea" || lines[0].Unit != 600 || lines[0].Total != 1200 || lines[0].WeightG != 200 {
		t.Fatalf("%+v %v", lines, err)
	}
	_ = cat.Remove("TEA-001")
	if _, err := c.Lines(); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("lines: %v", err)
	}
	if _, err := c.Subtotal(); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("subtotal: %v", err)
	}
	if _, err := c.WeightG(); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("weight: %v", err)
	}
	if err := c.Remove("TEA-001"); err != nil {
		t.Fatalf("a delisted line must still be removable: %v", err)
	}
}

func TestHiddenItemsIsACopy(t *testing.T) {
	c := New("c1", shelf(t))
	if got := c.Items(); got == nil || len(got) != 0 {
		t.Fatalf("%v", got)
	}
	_ = c.Add("TEA-001", 2)
	got := c.Items()
	got["TEA-001"] = 50
	got["MUG-001"] = 1
	if again := c.Items(); len(again) != 1 || again["TEA-001"] != 2 {
		t.Fatalf("%v", again)
	}
}
