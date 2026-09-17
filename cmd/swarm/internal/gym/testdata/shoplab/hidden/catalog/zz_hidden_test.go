package catalog

import (
	"errors"
	"sync"
	"testing"

	"shoplab/errs"
	"shoplab/sku"
)

func tea() Product {
	return Product{SKU: "TEA-001", Name: "Green Tea", Price: 505, WeightG: 120, Category: "food"}
}

func TestHiddenPutGetReplace(t *testing.T) {
	c := New()
	if err := c.Put(tea()); err != nil {
		t.Fatal(err)
	}
	got, err := c.Get("TEA-001")
	if err != nil || got != tea() {
		t.Fatalf("%+v %v", got, err)
	}
	p := tea()
	p.Price = 600
	if err := c.Put(p); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Get("TEA-001"); got.Price != 600 || c.Len() != 1 {
		t.Fatalf("%+v len=%d", got, c.Len())
	}
	free := Product{SKU: "FREE-1", Name: "Sticker", Price: 0, WeightG: 0, Category: "general"}
	if err := c.Put(free); err != nil {
		t.Fatalf("zero price and weight refused: %v", err)
	}
}

func TestHiddenPutRejects(t *testing.T) {
	c := New()
	bad := map[string]func(*Product){
		"lower-case sku": func(p *Product) { p.SKU = "tea-001" },
		"short sku":      func(p *Product) { p.SKU = "T" },
		"padded sku":     func(p *Product) { p.SKU = " TEA-001" },
		"no name":        func(p *Product) { p.Name = "" },
		"no category":    func(p *Product) { p.Category = "" },
		"negative price": func(p *Product) { p.Price = -1 },
		"negative grams": func(p *Product) { p.WeightG = -1 },
	}
	for name, breakIt := range bad {
		p := tea()
		breakIt(&p)
		if err := c.Put(p); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if c.Len() != 0 {
		t.Fatalf("len %d", c.Len())
	}
}

func TestHiddenNotFoundAndRemove(t *testing.T) {
	c := New()
	if _, err := c.Get("TEA-001"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("get: %v", err)
	}
	if err := c.Remove("TEA-001"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("remove: %v", err)
	}
	_ = c.Put(tea())
	if err := c.Remove("TEA-001"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get("TEA-001"); !errors.Is(err, errs.ErrNotFound) || c.Len() != 0 {
		t.Fatalf("after remove: %v len=%d", err, c.Len())
	}
}

func TestHiddenListSorted(t *testing.T) {
	c := New()
	if got := c.List(); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	for _, s := range []sku.SKU{"TEA-2", "MUG", "TEA-10", "ABC"} {
		p := tea()
		p.SKU = s
		if err := c.Put(p); err != nil {
			t.Fatal(err)
		}
	}
	var got []sku.SKU
	for _, p := range c.List() {
		got = append(got, p.SKU)
	}
	want := []sku.SKU{"ABC", "MUG", "TEA-10", "TEA-2"}
	if len(got) != 4 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("got %v", got)
	}
}

func TestHiddenConcurrent(t *testing.T) {
	c := New()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := tea()
			p.SKU = sku.SKU("SKU-" + string(rune('A'+i)))
			_ = c.Put(p)
			_, _ = c.Get(p.SKU)
			_ = c.List()
		}(i)
	}
	wg.Wait()
	if c.Len() != 20 {
		t.Fatalf("len %d", c.Len())
	}
}
