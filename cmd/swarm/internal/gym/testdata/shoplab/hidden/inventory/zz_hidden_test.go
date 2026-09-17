package inventory

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"shoplab/errs"
	"shoplab/sku"
)

const (
	tea sku.SKU = "TEA-001"
	mug sku.SKU = "MUG-001"
)

func TestHiddenReceiveAndCounts(t *testing.T) {
	v := New(time.Now)
	if v.OnHand(tea) != 0 || v.Available(tea) != 0 {
		t.Fatal("unknown sku is not zero")
	}
	for _, q := range []int{0, -3} {
		if err := v.Receive(tea, q); !errors.Is(err, errs.ErrInvalid) {
			t.Fatalf("receive %d: %v", q, err)
		}
	}
	_ = v.Receive(tea, 4)
	_ = v.Receive(tea, 6)
	if v.OnHand(tea) != 10 || v.Available(tea) != 10 {
		t.Fatalf("onhand %d available %d", v.OnHand(tea), v.Available(tea))
	}
}

func TestHiddenReserveAllOrNothing(t *testing.T) {
	v := New(time.Now)
	_ = v.Receive(tea, 10)
	_ = v.Receive(mug, 1)
	if err := v.Reserve("r1", map[sku.SKU]int{tea: 3, mug: 2}, 0); !errors.Is(err, errs.ErrInsufficient) {
		t.Fatalf("oversized: %v", err)
	}
	if v.Available(tea) != 10 || v.Available(mug) != 1 {
		t.Fatal("a failed reserve held stock")
	}
	if _, ok := v.Reserved("r1"); ok {
		t.Fatal("a failed reserve exists")
	}
	items := map[sku.SKU]int{tea: 3, mug: 1}
	if err := v.Reserve("r1", items, 0); err != nil {
		t.Fatal(err)
	}
	items[tea] = 99
	if v.Available(tea) != 7 || v.Available(mug) != 0 || v.OnHand(tea) != 10 {
		t.Fatalf("tea %d mug %d onhand %d", v.Available(tea), v.Available(mug), v.OnHand(tea))
	}
	got, ok := v.Reserved("r1")
	if !ok || len(got) != 2 || got[tea] != 3 || got[mug] != 1 {
		t.Fatalf("%v %v", got, ok)
	}
	got[tea] = 0
	if again, _ := v.Reserved("r1"); again[tea] != 3 {
		t.Fatal("Reserved handed out its own map")
	}
}

func TestHiddenReserveRejects(t *testing.T) {
	v := New(time.Now)
	_ = v.Receive(tea, 10)
	for name, err := range map[string]error{
		"empty id": v.Reserve("", map[sku.SKU]int{tea: 1}, 0),
		"no items": v.Reserve("r", nil, 0),
		"zero qty": v.Reserve("r", map[sku.SKU]int{tea: 0}, 0),
		"negative": v.Reserve("r", map[sku.SKU]int{tea: -1}, 0),
	} {
		if !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := v.Reserve("r", map[sku.SKU]int{tea: 1}, 0); err != nil {
		t.Fatal(err)
	}
	if err := v.Reserve("r", map[sku.SKU]int{tea: 1}, 0); !errors.Is(err, errs.ErrConflict) {
		t.Fatalf("duplicate id: %v", err)
	}
	if v.Available(tea) != 9 {
		t.Fatalf("available %d", v.Available(tea))
	}
}

func TestHiddenReleaseAndCommit(t *testing.T) {
	v := New(time.Now)
	_ = v.Receive(tea, 10)
	for _, err := range []error{v.Release("nope"), v.Commit("nope")} {
		if !errors.Is(err, errs.ErrNotFound) {
			t.Fatalf("unknown id: %v", err)
		}
	}
	_ = v.Reserve("a", map[sku.SKU]int{tea: 4}, 0)
	_ = v.Reserve("b", map[sku.SKU]int{tea: 5}, 0)
	if err := v.Release("a"); err != nil {
		t.Fatal(err)
	}
	if v.Available(tea) != 5 || v.OnHand(tea) != 10 {
		t.Fatalf("after release: %d/%d", v.Available(tea), v.OnHand(tea))
	}
	if err := v.Commit("b"); err != nil {
		t.Fatal(err)
	}
	if v.Available(tea) != 5 || v.OnHand(tea) != 5 {
		t.Fatalf("after commit: %d/%d", v.Available(tea), v.OnHand(tea))
	}
	if err := v.Commit("b"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("second commit: %v", err)
	}
	if err := v.Release("a"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("second release: %v", err)
	}
}

func TestHiddenReservationExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	v := New(func() time.Time { return now })
	_ = v.Receive(tea, 5)
	_ = v.Reserve("hold", map[sku.SKU]int{tea: 5}, time.Minute)
	_ = v.Receive(mug, 1)
	_ = v.Reserve("keep", map[sku.SKU]int{mug: 1}, 0)
	now = now.Add(time.Minute - time.Millisecond)
	if v.Available(tea) != 0 {
		t.Fatal("expired early")
	}
	now = now.Add(time.Millisecond)
	if v.Available(tea) != 5 || v.OnHand(tea) != 5 {
		t.Fatalf("at expiry: %d/%d", v.Available(tea), v.OnHand(tea))
	}
	if _, ok := v.Reserved("hold"); ok {
		t.Fatal("expired reservation still listed")
	}
	if err := v.Commit("hold"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("commit of expired: %v", err)
	}
	if err := v.Reserve("hold", map[sku.SKU]int{tea: 2}, time.Minute); err != nil {
		t.Fatalf("id not reusable: %v", err)
	}
	now = now.Add(1000 * time.Hour)
	if v.Available(mug) != 0 {
		t.Fatal("ttl 0 expired")
	}
}

func TestHiddenNoOversell(t *testing.T) {
	v := New(time.Now)
	_ = v.Receive(tea, 10)
	var ok, short atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := v.Reserve("r"+string(rune('0'+i)), map[sku.SKU]int{tea: 1}, 0)
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
	if ok.Load() != 10 || short.Load() != 40 || v.Available(tea) != 0 {
		t.Fatalf("ok=%d short=%d available=%d", ok.Load(), short.Load(), v.Available(tea))
	}
}
