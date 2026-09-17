package money

import (
	"errors"
	"reflect"
	"testing"

	"shoplab/errs"
)

func TestHiddenParse(t *testing.T) {
	for in, want := range map[string]Money{"12": 1200, "12.3": 1230, "12.34": 1234, "-0.05": -5, "0": 0, "007.10": 710, "-3": -300} {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", " 1", "1 ", "+1", ".5", "1.", "1.234", "1,00", "abc", "1.2.3", "--1", "-", "1e2"} {
		if _, err := Parse(in); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("Parse(%q) err = %v, want ErrInvalid", in, err)
		}
	}
}

func TestHiddenStringRoundTrip(t *testing.T) {
	for m, want := range map[Money]string{1234: "12.34", 0: "0.00", -5: "-0.05", 120000: "1200.00", 7: "0.07", -100: "-1.00"} {
		if got := m.String(); got != want {
			t.Errorf("%d -> %q, want %q", int64(m), got, want)
		}
		back, err := Parse(m.String())
		if err != nil || back != m {
			t.Errorf("round trip of %d gave %d, %v", int64(m), back, err)
		}
	}
}

func TestHiddenBPRoundsHalfAwayFromZero(t *testing.T) {
	for _, c := range []struct {
		m    Money
		bp   int64
		want Money
	}{
		{1005, 1000, 101}, {-1005, 1000, -101}, {1004, 1000, 100}, {333, 5000, 167}, {455, 500, 23},
		{999, 10000, 999}, {999, 0, 0}, {1, 4999, 0}, {1, 5000, 1},
	} {
		if got := c.m.BP(c.bp); got != c.want {
			t.Errorf("%d.BP(%d) = %d, want %d", c.m, c.bp, got, c.want)
		}
	}
}

func TestHiddenMulSumDiv(t *testing.T) {
	if got := Money(199).Mul(3); got != 597 {
		t.Fatalf("Mul %d", got)
	}
	if Sum() != 0 || Sum(1, 2, -5) != -2 {
		t.Fatal("Sum")
	}
	for _, c := range []struct {
		m    Money
		n    int64
		want Money
	}{{100, 3, 33}, {200, 3, 67}, {-5, 2, -3}, {5, 2, 3}, {0, 9, 0}, {7, 1, 7}} {
		got, err := c.m.Div(c.n)
		if err != nil || got != c.want {
			t.Errorf("%d.Div(%d) = %d, %v; want %d", c.m, c.n, got, err, c.want)
		}
	}
	for _, n := range []int64{0, -1} {
		if _, err := Money(10).Div(n); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("Div(%d) err = %v", n, err)
		}
	}
}

func TestHiddenAllocate(t *testing.T) {
	for _, c := range []struct {
		total   Money
		weights []int64
		want    []Money
	}{
		{100, []int64{1, 1, 1}, []Money{34, 33, 33}},
		{5, []int64{3, 7}, []Money{2, 3}},
		{150, []int64{1000, 505}, []Money{100, 50}},
		{10, []int64{0, 5, 0}, []Money{0, 10, 0}},
		{0, []int64{2, 3}, []Money{0, 0}},
		{101, []int64{1, 1}, []Money{51, 50}},
		{7, []int64{1, 2, 4}, []Money{1, 2, 4}},
	} {
		got, err := Allocate(c.total, c.weights)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("Allocate(%d, %v) = %v, %v; want %v", c.total, c.weights, got, err, c.want)
		}
	}
}

func TestHiddenAllocateErrorsAndPurity(t *testing.T) {
	for _, c := range []struct {
		total   Money
		weights []int64
	}{{10, nil}, {10, []int64{}}, {10, []int64{1, -1}}, {10, []int64{0, 0}}, {-1, []int64{1}}} {
		if _, err := Allocate(c.total, c.weights); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("Allocate(%d, %v) err = %v", c.total, c.weights, err)
		}
	}
	w := []int64{9, 1, 5}
	got, err := Allocate(1000, w)
	if err != nil || !reflect.DeepEqual(w, []int64{9, 1, 5}) {
		t.Fatalf("weights changed: %v %v", w, err)
	}
	if Sum(got...) != 1000 {
		t.Fatalf("parts %v do not add up", got)
	}
}
