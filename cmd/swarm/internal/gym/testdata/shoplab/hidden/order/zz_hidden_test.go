package order

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"shoplab/errs"
	"shoplab/pricing"
)

var (
	someLines = []Line{{SKU: "TEA-001", Qty: 2, Total: 1010}, {SKU: "MUG-001", Qty: 1, Total: 1000}}
	someQuote = pricing.Quote{Subtotal: 2010, Discount: 10, Tax: 150, Shipping: 500, Total: 2650}
)

func TestHiddenTransitionTable(t *testing.T) {
	all := []State{Pending, Paid, Shipped, Delivered, Cancelled, Refunded}
	allowed := map[[2]State]bool{
		{Pending, Paid}: true, {Pending, Cancelled}: true, {Paid, Shipped}: true, {Paid, Refunded}: true, {Shipped, Delivered}: true,
	}
	for _, from := range all {
		for _, to := range all {
			if got := CanTransition(from, to); got != allowed[[2]State{from, to}] {
				t.Errorf("%s -> %s = %v", from, to, got)
			}
		}
	}
	if Pending != "PENDING" || Paid != "PAID" || Shipped != "SHIPPED" || Delivered != "DELIVERED" || Cancelled != "CANCELLED" || Refunded != "REFUNDED" {
		t.Fatal("state values")
	}
}

func TestHiddenCreate(t *testing.T) {
	now := time.UnixMilli(9000)
	b := NewBook(func() time.Time { return now })
	for name, fn := range map[string]func() (Order, error){
		"no lines":       func() (Order, error) { return b.Create(nil, someQuote) },
		"zero qty":       func() (Order, error) { return b.Create([]Line{{SKU: "TEA-001", Qty: 0}}, someQuote) },
		"negative total": func() (Order, error) { return b.Create(someLines, pricing.Quote{Total: -1}) },
	} {
		if _, err := fn(); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	lines := append([]Line(nil), someLines...)
	o, err := b.Create(lines, someQuote)
	if err != nil || o.ID != "O-1" || o.State != Pending || o.Quote != someQuote || o.CreatedMs != 9000 || o.UpdatedMs != 9000 || len(o.Lines) != 2 {
		t.Fatalf("%+v %v", o, err)
	}
	lines[0].Qty = 77
	o.Lines[1].Qty = 88
	got, err := b.Get("O-1")
	if err != nil || got.Lines[0].Qty != 2 || got.Lines[1].Qty != 1 {
		t.Fatalf("book shares line storage: %+v %v", got, err)
	}
	if o2, _ := b.Create(someLines, pricing.Quote{}); o2.ID != "O-2" {
		t.Fatalf("second id %q", o2.ID)
	}
	if l := b.List(); len(l) != 2 || l[0].ID != "O-1" || l[1].ID != "O-2" {
		t.Fatalf("%+v", l)
	}
}

func TestHiddenHappyPath(t *testing.T) {
	now := time.UnixMilli(1000)
	b := NewBook(func() time.Time { return now })
	o, _ := b.Create(someLines, someQuote)
	now = now.Add(time.Second)
	if err := b.Pay(o.ID, 2649); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("short payment: %v", err)
	}
	if got, _ := b.Get(o.ID); got.State != Pending || got.UpdatedMs != 1000 {
		t.Fatalf("a refused payment changed the order: %+v", got)
	}
	if err := b.Pay(o.ID, 2650); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.Get(o.ID); got.State != Paid || got.UpdatedMs != 2000 || got.CreatedMs != 1000 {
		t.Fatalf("%+v", got)
	}
	now = now.Add(time.Second)
	if err := b.Ship(o.ID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Second)
	if err := b.Deliver(o.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := b.Get(o.ID); got.State != Delivered || got.UpdatedMs != 4000 {
		t.Fatalf("%+v", got)
	}
}

func TestHiddenCancelAndRefund(t *testing.T) {
	b := NewBook(time.Now)
	a, _ := b.Create(someLines, someQuote)
	c, _ := b.Create(someLines, someQuote)
	if st, err := b.Cancel(a.ID); err != nil || st != Cancelled {
		t.Fatalf("%s %v", st, err)
	}
	_ = b.Pay(c.ID, 2650)
	if st, err := b.Cancel(c.ID); err != nil || st != Refunded {
		t.Fatalf("%s %v", st, err)
	}
	if got, _ := b.Get(c.ID); got.State != Refunded {
		t.Fatalf("%+v", got)
	}
	for _, id := range []string{a.ID, c.ID} {
		if _, err := b.Cancel(id); !errors.Is(err, errs.ErrState) {
			t.Fatalf("cancel twice %s: %v", id, err)
		}
	}
}

func TestHiddenWrongStateAndUnknown(t *testing.T) {
	b := NewBook(time.Now)
	o, _ := b.Create(someLines, someQuote)
	if err := b.Ship(o.ID); !errors.Is(err, errs.ErrState) {
		t.Fatalf("ship pending: %v", err)
	}
	if err := b.Deliver(o.ID); !errors.Is(err, errs.ErrState) {
		t.Fatalf("deliver pending: %v", err)
	}
	_ = b.Pay(o.ID, 2650)
	if err := b.Pay(o.ID, 1); !errors.Is(err, errs.ErrState) {
		t.Fatalf("state is checked before amount: %v", err)
	}
	_ = b.Ship(o.ID)
	if _, err := b.Cancel(o.ID); !errors.Is(err, errs.ErrState) {
		t.Fatalf("cancel shipped: %v", err)
	}
	_, e1 := b.Get("O-9")
	_, e2 := b.Cancel("O-9")
	for _, err := range []error{e1, e2, b.Pay("O-9", 1), b.Ship("O-9"), b.Deliver("O-9")} {
		if !errors.Is(err, errs.ErrNotFound) {
			t.Fatalf("unknown id: %v", err)
		}
	}
}

func TestHiddenConcurrentPayOnce(t *testing.T) {
	b := NewBook(time.Now)
	o, _ := b.Create(someLines, someQuote)
	var ok, late atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch err := b.Pay(o.ID, 2650); {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, errs.ErrState):
				late.Add(1)
			default:
				t.Error(err)
			}
			_, _ = b.Create(someLines, someQuote)
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || late.Load() != 19 || len(b.List()) != 21 {
		t.Fatalf("ok=%d late=%d orders=%d", ok.Load(), late.Load(), len(b.List()))
	}
}
