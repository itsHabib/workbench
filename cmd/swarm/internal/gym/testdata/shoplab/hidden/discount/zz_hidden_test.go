package discount

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"shoplab/errs"
	"shoplab/money"
)

func TestHiddenAddRejects(t *testing.T) {
	b := New(time.Now)
	for name, c := range map[string]Coupon{
		"no code":      {PercentBP: 1000},
		"neither":      {Code: "A"},
		"both":         {Code: "A", PercentBP: 1000, Fixed: 100},
		"pct too big":  {Code: "A", PercentBP: 10001},
		"pct negative": {Code: "A", PercentBP: -1},
		"fixed neg":    {Code: "A", Fixed: -100},
		"min neg":      {Code: "A", Fixed: 100, MinSubtotal: -1},
		"uses neg":     {Code: "A", Fixed: 100, MaxUses: -1},
	} {
		if err := b.Add(c); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := b.Add(Coupon{Code: "save", PercentBP: 10000}); err != nil {
		t.Fatal(err)
	}
	if err := b.Add(Coupon{Code: "SAVE", Fixed: 100}); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestHiddenPercentAndFixed(t *testing.T) {
	b := New(time.Now)
	_ = b.Add(Coupon{Code: "TEN", PercentBP: 1000})
	_ = b.Add(Coupon{Code: "FIVER", Fixed: 500})
	for _, c := range []struct {
		code string
		sub  money.Money
		want money.Money
	}{{"TEN", 1005, 101}, {"ten", 1004, 100}, {"TEN", 0, 0}, {"FIVER", 2000, 500}, {"fiver", 300, 300}, {"FIVER", 0, 0}} {
		got, err := b.Quote(c.code, c.sub)
		if err != nil || got != c.want {
			t.Errorf("Quote(%s, %d) = %d, %v; want %d", c.code, c.sub, got, err, c.want)
		}
	}
	if _, err := b.Quote("NOPE", 100); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := b.Quote("TEN", -1); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("negative subtotal: %v", err)
	}
}

func TestHiddenMinSubtotalAndExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	b := New(func() time.Time { return now })
	_ = b.Add(Coupon{Code: "BIG", Fixed: 500, MinSubtotal: 2000, Expires: now.Add(time.Hour)})
	if _, err := b.Quote("BIG", 1999); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("below min: %v", err)
	}
	if got, err := b.Quote("BIG", 2000); err != nil || got != 500 {
		t.Fatalf("at min: %d %v", got, err)
	}
	now = now.Add(time.Hour - time.Second)
	if _, err := b.Redeem("BIG", 2000); err != nil {
		t.Fatalf("just before expiry: %v", err)
	}
	now = now.Add(time.Second)
	if _, err := b.Quote("BIG", 2000); !errors.Is(err, errs.ErrState) {
		t.Fatalf("at expiry: %v", err)
	}
	if _, err := b.Redeem("BIG", 1); !errors.Is(err, errs.ErrState) {
		t.Fatalf("expired wins over min subtotal: %v", err)
	}
	if b.Uses("BIG") != 1 {
		t.Fatalf("uses %d", b.Uses("BIG"))
	}
}

func TestHiddenQuoteDoesNotConsume(t *testing.T) {
	b := New(time.Now)
	_ = b.Add(Coupon{Code: "ONCE", Fixed: 100, MaxUses: 1, MinSubtotal: 50})
	for i := 0; i < 5; i++ {
		if _, err := b.Quote("ONCE", 1000); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Redeem("ONCE", 10); !errors.Is(err, errs.ErrInvalid) || b.Uses("ONCE") != 0 {
		t.Fatalf("failed redeem counted: %v uses=%d", err, b.Uses("ONCE"))
	}
	if got, err := b.Redeem("once", 1000); err != nil || got != 100 || b.Uses("Once") != 1 {
		t.Fatalf("%d %v uses=%d", got, err, b.Uses("Once"))
	}
	if _, err := b.Quote("ONCE", 1000); !errors.Is(err, errs.ErrState) {
		t.Fatalf("exhausted quote: %v", err)
	}
	if _, err := b.Redeem("ONCE", 10); !errors.Is(err, errs.ErrState) {
		t.Fatalf("exhausted wins over min subtotal: %v", err)
	}
	if b.Uses("NOPE") != 0 {
		t.Fatal("uses of unknown code")
	}
}

func TestHiddenMaxUsesUnderConcurrency(t *testing.T) {
	b := New(time.Now)
	_ = b.Add(Coupon{Code: "FIVE", PercentBP: 500, MaxUses: 5})
	var ok, spent atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := b.Redeem("FIVE", 1000)
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, errs.ErrState):
				spent.Add(1)
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 5 || spent.Load() != 25 || b.Uses("FIVE") != 5 {
		t.Fatalf("ok=%d spent=%d uses=%d", ok.Load(), spent.Load(), b.Uses("FIVE"))
	}
}
