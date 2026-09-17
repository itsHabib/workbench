package app

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"shoplab/errs"
	"shoplab/outbox"
)

func exec(t *testing.T, a *App, line, want string) {
	t.Helper()
	got, err := a.Exec(line)
	if err != nil || got != want {
		t.Fatalf("%q -> %q (%v), want %q", line, got, err, want)
	}
}

func fails(t *testing.T, a *App, line string, want error) {
	t.Helper()
	got, err := a.Exec(line)
	if !errors.Is(err, want) || got != "" {
		t.Fatalf("%q -> %q (%v), want %v", line, got, err, want)
	}
}

// shop has a 10.00 mug (general, 600 g, 5 in stock) and a 5.05 tea (food, 600 g, 3 in stock).
func shop(t *testing.T, now func() time.Time, events *bytes.Buffer) *App {
	t.Helper()
	var a *App
	if events == nil {
		a = New(now, nil)
	} else {
		a = New(now, events)
	}
	exec(t, a, "PRODUCT mug-001 Mug 10.00 600 general", "OK")
	exec(t, a, `product tea-001 "Green Tea" 5.05 600 food`, "OK")
	exec(t, a, "STOCK mug-001 2", "2")
	exec(t, a, "STOCK MUG-001 3", "5")
	exec(t, a, "STOCK tea-001 3", "3")
	return a
}

func TestHiddenHappyPath(t *testing.T) {
	a := shop(t, time.Now, nil)
	exec(t, a, "COUPON save FIXED 1.50", "OK")
	exec(t, a, "COUPON ten pct 1000", "OK")
	exec(t, a, "ADD c1 mug-001 1", "10.00")
	exec(t, a, "ADD c1 tea-001 1", "15.05")
	exec(t, a, "SHOW c1", "MUG-001 1 10.00\nTEA-001 1 5.05\nsubtotal 15.05")
	exec(t, a, "CHECKOUT c1 dom SAVE", "O-1 21.18")
	fails(t, a, "SHOW c1", errs.ErrNotFound)
	exec(t, a, "AVAIL mug-001", "4")
	exec(t, a, "AVAIL tea-001", "2")
	exec(t, a, "ORDER O-1", "PENDING 21.18")
	exec(t, a, "PAY O-1 21.18", "PAID")
	exec(t, a, "BALANCE cash", "21.18")
	exec(t, a, "BALANCE revenue", "-13.55")
	exec(t, a, "BALANCE tax", "-1.13")
	exec(t, a, "BALANCE shipping", "-6.50")
	exec(t, a, "BALANCE nothing", "0.00")
	exec(t, a, "SHIP O-1", "SHIPPED")
	exec(t, a, "DELIVER O-1", "DELIVERED")
	exec(t, a, "ORDER O-1", "DELIVERED 21.18")
	exec(t, a, "AVAIL mug-001", "4")

	// Percent coupon: 1.51 off, split 1.00 / 0.51, tax 0.90 + 0.23.
	exec(t, a, "ADD c1 tea-001 1", "5.05")
	exec(t, a, "ADD c1 mug-001 1", "15.05")
	exec(t, a, "CHECKOUT c1 DOM ten", "O-2 21.17")
	// International: no tax, 1200 g is 19.00.
	exec(t, a, "ADD c9 tea-001 1", "5.05")
	exec(t, a, "ADD c9 mug-001 1", "15.05")
	exec(t, a, "CHECKOUT c9 intl", "O-3 34.05")
	exec(t, a, "PAY O-3 34.05", "PAID")
	exec(t, a, "BALANCE tax", "-1.13")
	exec(t, a, "BALANCE shipping", "-25.50")
	exec(t, a, "BALANCE cash", "55.23")
	exec(t, a, "REPORT", "orders=3 gross=55.23 refunded=0.00 net=55.23 aov=27.62")
	exec(t, a, "AVAIL tea-001", "0")
}

func TestHiddenErrorsChangeNothing(t *testing.T) {
	a := shop(t, time.Now, nil)
	exec(t, a, "COUPON big FIXED 5.00 100.00", "OK")
	for line, want := range map[string]error{
		"":                                     errs.ErrInvalid,
		"BOGUS":                                errs.ErrInvalid,
		"ADD c1 mug-001":                       errs.ErrInvalid,
		"ADD c1 mug-001 many":                  errs.ErrInvalid,
		"ADD c1 mug-001 0":                     errs.ErrInvalid,
		"ADD c1 m 1":                           errs.ErrInvalid,
		"ADD c1 nope-1 1":                      errs.ErrNotFound,
		"STOCK nope-1 1":                       errs.ErrNotFound,
		"STOCK mug-001 0":                      errs.ErrInvalid,
		"AVAIL nope-1":                         errs.ErrNotFound,
		"PRODUCT x Thing 1.00 1 general":       errs.ErrInvalid,
		"PRODUCT abc Thing 1.234 1 general":    errs.ErrInvalid,
		"PRODUCT abc Thing 1.00 heavy general": errs.ErrInvalid,
		"PRODUCT abc Thing -1.00 1 general":    errs.ErrInvalid,
		"COUPON c PCT 0":                       errs.ErrInvalid,
		"COUPON c PCT 10001":                   errs.ErrInvalid,
		"COUPON c HALF 5":                      errs.ErrInvalid,
		"COUPON BIG PCT 500":                   errs.ErrConflict,
		"CHECKOUT ghost dom":                   errs.ErrNotFound,
		"REMOVE ghost mug-001":                 errs.ErrNotFound,
		"ORDER O-1":                            errs.ErrNotFound,
		"PAY O-1 1.00":                         errs.ErrNotFound,
		"SHIP O-1":                             errs.ErrNotFound,
		"CANCEL O-1":                           errs.ErrNotFound,
	} {
		fails(t, a, line, want)
	}
	fails(t, a, "SHOW c1", errs.ErrNotFound) // no ADD to c1 ever succeeded

	exec(t, a, "ADD c1 tea-001 4", "20.20")
	fails(t, a, "CHECKOUT c1 mars", errs.ErrInvalid)
	fails(t, a, "CHECKOUT c1 dom NOSUCH", errs.ErrNotFound)
	fails(t, a, "CHECKOUT c1 dom big", errs.ErrInvalid) // below the coupon's minimum
	fails(t, a, "CHECKOUT c1 dom", errs.ErrInsufficient)
	exec(t, a, "SHOW c1", "TEA-001 4 20.20\nsubtotal 20.20")
	exec(t, a, "AVAIL tea-001", "3")
	fails(t, a, "REMOVE c1 mug-001", errs.ErrNotFound)
	exec(t, a, "REMOVE c1 tea-001", "0.00")
	exec(t, a, "SHOW c1", "subtotal 0.00")
	fails(t, a, "CHECKOUT c1 dom", errs.ErrInvalid) // empty cart
	exec(t, a, "ADD c1 tea-001 3", "15.15")
	// Failed checkouts used up no order id: 15.15 + 0.76 tax + 8.00 for 1800 g.
	exec(t, a, "CHECKOUT c1 dom", "O-1 23.91")
	exec(t, a, "REPORT", "orders=1 gross=0.00 refunded=0.00 net=0.00 aov=0.00")
	if n := len(a.Events()); n != 1 {
		t.Fatalf("%d events", n)
	}
}

func TestHiddenReservationExpiry(t *testing.T) {
	now := time.Unix(50000, 0)
	a := shop(t, func() time.Time { return now }, nil)
	exec(t, a, "ADD c1 tea-001 3", "15.15")
	exec(t, a, "CHECKOUT c1 dom", "O-1 23.91")
	exec(t, a, "ADD c2 mug-001 5", "50.00")
	exec(t, a, "CHECKOUT c2 dom", "O-2 55.00")
	exec(t, a, "AVAIL tea-001", "0")
	exec(t, a, "ADD c3 tea-001 1", "5.05")
	fails(t, a, "CHECKOUT c3 dom", errs.ErrInsufficient)

	now = now.Add(15*time.Minute - time.Second)
	fails(t, a, "PAY O-2 54.99", errs.ErrInvalid)
	exec(t, a, "ORDER O-2", "PENDING 55.00")
	exec(t, a, "PAY O-2 55.00", "PAID")
	exec(t, a, "AVAIL tea-001", "0")

	now = now.Add(time.Second)
	exec(t, a, "AVAIL tea-001", "3")
	exec(t, a, "AVAIL mug-001", "0")
	exec(t, a, "ORDER O-1", "PENDING 23.91")
	fails(t, a, "PAY O-1 1.00", errs.ErrInvalid) // amount is checked before the reservation
	exec(t, a, "ORDER O-1", "PENDING 23.91")
	fails(t, a, "PAY O-1 23.91", errs.ErrState)
	exec(t, a, "ORDER O-1", "CANCELLED 23.91")
	fails(t, a, "PAY O-1 23.91", errs.ErrState)
	exec(t, a, "BALANCE cash", "55.00")
	exec(t, a, "BALANCE shipping", "0.00")
	exec(t, a, "CHECKOUT c3 dom", "O-3 10.30")
	evs := a.Events()
	if last := evs[len(evs)-2]; last.Kind != "order.cancelled" || last.Fields["order"] != "O-1" {
		t.Fatalf("%+v", evs)
	}
}

func TestHiddenCancelAndRefund(t *testing.T) {
	a := shop(t, time.Now, nil)
	exec(t, a, "ADD c1 tea-001 2", "10.10")
	exec(t, a, "CHECKOUT c1 dom", "O-1 17.11")
	exec(t, a, "AVAIL tea-001", "1")
	exec(t, a, "CANCEL O-1", "CANCELLED")
	exec(t, a, "AVAIL tea-001", "3")
	fails(t, a, "CANCEL O-1", errs.ErrState)
	fails(t, a, "PAY O-1 17.11", errs.ErrState)

	exec(t, a, "ADD c1 tea-001 2", "10.10")
	exec(t, a, "CHECKOUT c1 dom", "O-2 17.11")
	exec(t, a, "PAY O-2 17.11", "PAID")
	exec(t, a, "BALANCE tax", "-0.51")
	exec(t, a, "CANCEL O-2", "REFUNDED")
	exec(t, a, "ORDER O-2", "REFUNDED 17.11")
	for _, acct := range []string{"cash", "revenue", "tax", "shipping"} {
		exec(t, a, "BALANCE "+acct, "0.00")
	}
	exec(t, a, "AVAIL tea-001", "3")
	exec(t, a, "REPORT", "orders=2 gross=17.11 refunded=17.11 net=0.00 aov=0.00")

	exec(t, a, "ADD c1 mug-001 1", "10.00")
	exec(t, a, "CHECKOUT c1 dom", "O-3 16.00")
	exec(t, a, "PAY O-3 16.00", "PAID")
	exec(t, a, "SHIP O-3", "SHIPPED")
	fails(t, a, "CANCEL O-3", errs.ErrState)
	fails(t, a, "SHIP O-3", errs.ErrState)
	exec(t, a, "AVAIL mug-001", "4")
	exec(t, a, "REPORT", "orders=3 gross=33.11 refunded=17.11 net=16.00 aov=16.00")
}

func TestHiddenIdempotency(t *testing.T) {
	now := time.Unix(50000, 0)
	a := shop(t, func() time.Time { return now }, nil)
	fails(t, a, "AVAIL nope-1 @probe", errs.ErrNotFound)
	exec(t, a, "PRODUCT nope-1 Nope 1.00 1 general", "OK")
	exec(t, a, "AVAIL nope-1 @probe", "0") // the failure was not remembered
	exec(t, a, "STOCK nope-1 7", "7")
	exec(t, a, "AVAIL nope-1 @probe", "0") // the success was

	exec(t, a, "ADD c1 mug-001 1", "10.00")
	exec(t, a, "CHECKOUT c1 dom @co-1", "O-1 16.00")
	exec(t, a, "CHECKOUT c1 dom @co-1", "O-1 16.00")
	exec(t, a, "@co-1 REPORT", "O-1 16.00") // the key decides, not the line
	fails(t, a, "ORDER O-2", errs.ErrNotFound)
	exec(t, a, "AVAIL mug-001", "4")

	exec(t, a, "PAY O-1 16.00 @pay-1", "PAID")
	exec(t, a, "PAY O-1 16.00 @pay-1", "PAID")
	exec(t, a, "BALANCE cash", "16.00")
	fails(t, a, "PAY O-1 16.00", errs.ErrState)
	fails(t, a, "PAY O-1 16.00 @pay-2", errs.ErrState)

	now = now.Add(24*time.Hour - time.Second)
	exec(t, a, "REPORT @co-1", "O-1 16.00")
	now = now.Add(time.Second)
	exec(t, a, "REPORT @co-1", "orders=1 gross=16.00 refunded=0.00 net=16.00 aov=16.00")
	if n := len(a.Events()); n != 2 {
		t.Fatalf("%d events, want order.created and order.paid once each", n)
	}
}

func TestHiddenEventLog(t *testing.T) {
	now := time.UnixMilli(1_000_000)
	var buf bytes.Buffer
	a := shop(t, func() time.Time { return now }, &buf)
	step := func(line, want string) {
		t.Helper()
		now = now.Add(time.Second)
		exec(t, a, line, want)
	}
	step("ADD c1 mug-001 1", "10.00")
	step("CHECKOUT c1 dom", "O-1 16.00")
	step("PAY O-1 16.00", "PAID")
	step("SHIP O-1", "SHIPPED")
	step("DELIVER O-1", "DELIVERED")
	step("ADD c1 mug-001 1", "10.00")
	step("CHECKOUT c1 intl", "O-2 25.00")
	step("CANCEL O-2", "CANCELLED")
	step("ADD c1 mug-001 1", "10.00")
	step("CHECKOUT c1 intl", "O-3 25.00")
	step("PAY O-3 25.00", "PAID")
	step("CANCEL O-3", "REFUNDED")
	fails(t, a, "SHIP O-3", errs.ErrState)

	ev := func(seq int64, sec int64, kind, order, total string) outbox.Event {
		f := map[string]string{"order": order}
		if total != "" {
			f["total"] = total
		}
		return outbox.Event{Seq: seq, AtMs: 1_000_000 + sec*1000, Kind: kind, Fields: f}
	}
	want := []outbox.Event{
		ev(1, 2, "order.created", "O-1", "16.00"), ev(2, 3, "order.paid", "O-1", ""), ev(3, 4, "order.shipped", "O-1", ""),
		ev(4, 5, "order.delivered", "O-1", ""), ev(5, 7, "order.created", "O-2", "25.00"), ev(6, 8, "order.cancelled", "O-2", ""),
		ev(7, 10, "order.created", "O-3", "25.00"), ev(8, 11, "order.paid", "O-3", ""), ev(9, 12, "order.refunded", "O-3", ""),
	}
	if got := a.Events(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events\n got %+v\nwant %+v", got, want)
	}
	written, err := outbox.ReadAll(&buf)
	if err != nil || !reflect.DeepEqual(written, want) {
		t.Fatalf("written\n got %+v (%v)\nwant %+v", written, err, want)
	}
}

func TestHiddenConcurrentCheckoutsNeverOversell(t *testing.T) {
	a := shop(t, time.Now, nil)
	var ok, short atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cart := fmt.Sprintf("cart-%d", i)
			if _, err := a.Exec("ADD " + cart + " mug-001 1"); err != nil {
				t.Error(err)
				return
			}
			_, err := a.Exec("CHECKOUT " + cart + " dom")
			switch {
			case err == nil:
				ok.Add(1)
			case errors.Is(err, errs.ErrInsufficient):
				short.Add(1)
			default:
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if ok.Load() != 5 || short.Load() != 19 {
		t.Fatalf("ok=%d short=%d", ok.Load(), short.Load())
	}
	exec(t, a, "AVAIL mug-001", "0")
	if n := len(a.Events()); n != 5 {
		t.Fatalf("%d events", n)
	}
}
