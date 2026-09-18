package ids

import (
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"schedlab/clock"
	"schedlab/errs"
)

func TestHiddenShape(t *testing.T) {
	g := New(clock.NewFake(time.UnixMilli(1000)))
	id := g.Next()
	if id != "0000000003e8-000000" || id.String() != "0000000003e8-000000" || len(id) != 19 {
		t.Fatalf("%q", id)
	}
	if got := g.Next(); got != "0000000003e8-000001" {
		t.Fatalf("second %q", got)
	}
	at, err := id.Time()
	if err != nil || !at.Equal(time.UnixMilli(1000)) || at.Location() != time.UTC {
		t.Fatalf("%v %v", at, err)
	}
}

func TestHiddenParse(t *testing.T) {
	for _, s := range []string{"0000000003e8-000000", "ffffffffffff-ffffff", "000000000000-000000"} {
		if id, err := Parse(s); err != nil || string(id) != s {
			t.Errorf("%q: %v", s, err)
		}
	}
	for _, s := range []string{
		"", "0000000003e8-00000", "0000000003e8-0000000", "0000000003E8-000000", "0000000003e8_000000",
		"0000000003e8-00000g", " 000000003e8-000000", "0000000003e8-000000\n",
	} {
		if _, err := Parse(s); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("%q: %v", s, err)
		}
	}
	if _, err := ID("nope").Time(); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("time of bad id: %v", err)
	}
}

func TestHiddenMonotonicAcrossClock(t *testing.T) {
	f := clock.NewFake(time.UnixMilli(5000))
	g := New(f)
	var got []ID
	got = append(got, g.Next(), g.Next())
	f.Advance(time.Millisecond)
	got = append(got, g.Next())
	f.Set(time.UnixMilli(100))
	got = append(got, g.Next(), g.Next())
	f.Set(time.UnixMilli(7000))
	got = append(got, g.Next())
	want := []ID{
		"000000001388-000000", "000000001388-000001", "000000001389-000000",
		"000000001389-000001", "000000001389-000002", "000000001b58-000000",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("id %d = %q, want %q", i, got[i], want[i])
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("not increasing at %d: %q %q", i, got[i-1], got[i])
		}
	}
	neg := New(clock.NewFake(time.UnixMilli(-50)))
	if id := neg.Next(); id != "000000000000-000000" {
		t.Fatalf("negative clock %q", id)
	}
}

func TestHiddenSort(t *testing.T) {
	ids := []ID{"000000001389-000000", "000000001388-000001", "000000001388-000000"}
	Sort(ids)
	if ids[0] != "000000001388-000000" || ids[1] != "000000001388-000001" || ids[2] != "000000001389-000000" {
		t.Fatalf("%v", ids)
	}
	g := New(clock.NewFake(time.UnixMilli(1)))
	made := []ID{g.Next(), g.Next(), g.Next()}
	shuffled := []ID{made[2], made[0], made[1]}
	Sort(shuffled)
	for i := range made {
		if shuffled[i] != made[i] {
			t.Fatalf("%v != %v", shuffled, made)
		}
	}
}

func TestHiddenConcurrentUnique(t *testing.T) {
	g := New(clock.NewFake(time.UnixMilli(42)))
	var mu sync.Mutex
	var wg sync.WaitGroup
	seen := map[ID]bool{}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				id := g.Next()
				mu.Lock()
				if seen[id] {
					t.Errorf("duplicate %q", id)
				}
				seen[id] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != 1600 {
		t.Fatalf("%d ids", len(seen))
	}
	all := make([]string, 0, len(seen))
	for id := range seen {
		all = append(all, string(id))
	}
	sort.Strings(all)
	if all[0] != "00000000002a-000000" || all[len(all)-1] != "00000000002a-00063f" {
		t.Fatalf("range %q..%q", all[0], all[len(all)-1])
	}
}
