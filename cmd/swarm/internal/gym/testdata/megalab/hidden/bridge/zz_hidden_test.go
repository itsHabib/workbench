package bridge

import (
	"strings"
	"testing"
	"time"

	"megalab/sched/clock"
	"megalab/sched/state"
)

func fresh(t *testing.T) *Bridge {
	t.Helper()
	b, err := New(clock.NewFake(time.Unix(1_700_000_000, 0)), nil)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func ok(t *testing.T, b *Bridge, line string) string {
	t.Helper()
	reply, err := b.Exec(line)
	if err != nil {
		t.Fatalf("%s: %v", line, err)
	}
	return reply
}

func TestHiddenCommandsRemembered(t *testing.T) {
	b := fresh(t)
	if r := ok(t, b, "PRODUCT WID-1 widget 5.00 100 general"); r != "OK" {
		t.Fatalf("reply %q", r)
	}
	if _, err := b.Exec("STOCK NOPE 3"); err == nil {
		t.Fatal("unknown sku accepted")
	}
	ok(t, b, "STOCK WID-1 3")
	if b.Count() != 2 {
		t.Fatalf("count %d", b.Count())
	}
	if v, _ := b.Store().Get("cmd:1"); v != "PRODUCT WID-1 widget 5.00 100 general" {
		t.Fatalf("cmd:1 = %q", v)
	}
	if v, _ := b.Store().Get("reply:2"); v != "3" {
		t.Fatalf("reply:2 = %q", v)
	}
	if _, present := b.Store().Get("cmd:3"); present {
		t.Fatal("failed command remembered")
	}
}

func paidOrder(t *testing.T, b *Bridge) string {
	t.Helper()
	ok(t, b, "PRODUCT WID-1 widget 5.00 100 general")
	ok(t, b, "STOCK WID-1 3")
	ok(t, b, "ADD c1 WID-1 2")
	reply := ok(t, b, "CHECKOUT c1 DOM")
	order := strings.Fields(reply)[0]
	total := ok(t, b, "ORDER "+order)
	amount := strings.Fields(total)[1]
	ok(t, b, "PAY "+order+" "+amount)
	return order
}

func TestHiddenPaidOrderBecomesRun(t *testing.T) {
	b := fresh(t)
	order := paidOrder(t, b)
	st, has := b.Fulfilment(order)
	if !has || st != state.Pending {
		t.Fatalf("fulfilment %v %v", st, has)
	}
	id, present := b.Store().Get("fulfil:" + order)
	if !present || id == "" || id == "done" {
		t.Fatalf("fulfil key %q %v", id, present)
	}
	if _, has := b.Fulfilment("nope"); has {
		t.Fatal("unknown order has a run")
	}
}

func TestHiddenThreeTicksFulfil(t *testing.T) {
	b := fresh(t)
	order := paidOrder(t, b)
	for i := 0; i < 3; i++ {
		if err := b.Tick(); err != nil {
			t.Fatal(err)
		}
	}
	if st, _ := b.Fulfilment(order); st != state.Succeeded {
		t.Fatalf("after three ticks: %v", st)
	}
	if v, _ := b.Store().Get("fulfil:" + order); v != "done" {
		t.Fatalf("fulfil:%s = %q", order, v)
	}
}

func TestHiddenFailedPayChangesNothing(t *testing.T) {
	b := fresh(t)
	ok(t, b, "PRODUCT WID-1 widget 5.00 100 general")
	ok(t, b, "STOCK WID-1 3")
	ok(t, b, "ADD c1 WID-1 1")
	reply := ok(t, b, "CHECKOUT c1 DOM")
	order := strings.Fields(reply)[0]
	before := b.Count()
	if _, err := b.Exec("PAY " + order + " 0.01"); err == nil {
		t.Fatal("wrong amount accepted")
	}
	if b.Count() != before {
		t.Fatal("failed PAY counted")
	}
	if _, has := b.Fulfilment(order); has {
		t.Fatal("failed PAY submitted a run")
	}
}

func TestHiddenCaseInsensitivePay(t *testing.T) {
	b := fresh(t)
	ok(t, b, "PRODUCT WID-1 widget 5.00 100 general")
	ok(t, b, "STOCK WID-1 3")
	ok(t, b, "ADD c1 WID-1 1")
	order := strings.Fields(ok(t, b, "CHECKOUT c1 DOM"))[0]
	amount := strings.Fields(ok(t, b, "ORDER "+order))[1]
	ok(t, b, "pay "+order+" "+amount)
	if _, has := b.Fulfilment(order); !has {
		t.Fatal("lower-case pay did not submit")
	}
}
