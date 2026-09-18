package retry

import (
	"errors"
	"testing"
	"time"

	"schedlab/errs"
)

var pol = Policy{Base: time.Second, Max: 5 * time.Second, Factor: 2, Jitter: 0.5, MaxAttempts: 5}

func fixed(u float64) func() float64 { return func() float64 { return u } }

func TestHiddenValidate(t *testing.T) {
	if err := pol.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := Default.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []Policy{
		{Base: 0, Max: time.Second, Factor: 2, MaxAttempts: 1},
		{Base: -time.Second, Max: time.Second, Factor: 2, MaxAttempts: 1},
		{Base: 2 * time.Second, Max: time.Second, Factor: 2, MaxAttempts: 1},
		{Base: time.Second, Max: time.Second, Factor: 0.99, MaxAttempts: 1},
		{Base: time.Second, Max: time.Second, Factor: 1, Jitter: -0.01, MaxAttempts: 1},
		{Base: time.Second, Max: time.Second, Factor: 1, Jitter: 1.01, MaxAttempts: 1},
		{Base: time.Second, Max: time.Second, Factor: 1, MaxAttempts: 0},
	}
	for i, p := range bad {
		if err := p.Validate(); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("policy %d: %v", i, err)
		}
	}
	edge := Policy{Base: time.Second, Max: time.Second, Factor: 1, Jitter: 1, MaxAttempts: 1}
	if err := edge.Validate(); err != nil {
		t.Fatalf("edge values: %v", err)
	}
}

func TestHiddenRetryWindow(t *testing.T) {
	for attempt, want := range map[int]bool{-1: false, 0: false, 1: true, 4: true, 5: false, 6: false} {
		if got := pol.Retry(attempt); got != want {
			t.Errorf("Retry(%d) = %v", attempt, got)
		}
	}
	one := Policy{Base: time.Second, Max: time.Second, Factor: 1, MaxAttempts: 1}
	if one.Retry(1) {
		t.Fatal("a single attempt policy never retries")
	}
	if !Default.Retry(2) || Default.Retry(3) {
		t.Fatal("default allows exactly three attempts")
	}
}

func TestHiddenDelayExamples(t *testing.T) {
	cases := []struct {
		attempt int
		u       float64
		want    time.Duration
	}{
		{1, 0.5, time.Second},
		{2, 0.5, 2 * time.Second},
		{3, 0, 2 * time.Second},
		{3, 0.5, 4 * time.Second},
		{4, 1, 7500 * time.Millisecond},
		{4, 0.5, 5 * time.Second},
		{1, 0, 500 * time.Millisecond},
		{2, 0.25, 1500 * time.Millisecond},
	}
	for _, c := range cases {
		got, err := pol.Delay(c.attempt, fixed(c.u))
		if err != nil || got != c.want {
			t.Errorf("attempt %d u %v: %v %v, want %v", c.attempt, c.u, got, err, c.want)
		}
	}
}

func TestHiddenDelayNilRandAndNoJitter(t *testing.T) {
	got, err := pol.Delay(2, nil)
	if err != nil || got != 2*time.Second {
		t.Fatalf("nil rnd: %v %v", got, err)
	}
	calls := 0
	got, err = Default.Delay(2, func() float64 { calls++; return 0.999 })
	if err != nil || got != 2*time.Second || calls != 1 {
		t.Fatalf("default: %v %v calls=%d", got, err, calls)
	}
	capped := Policy{Base: time.Second, Max: 3 * time.Second, Factor: 10, MaxAttempts: 10}
	if got, _ := capped.Delay(5, nil); got != 3*time.Second {
		t.Fatalf("cap: %v", got)
	}
}

func TestHiddenDelayErrors(t *testing.T) {
	for _, attempt := range []int{0, -3} {
		if _, err := pol.Delay(attempt, nil); !errors.Is(err, errs.ErrInvalid) {
			t.Errorf("attempt %d: %v", attempt, err)
		}
	}
	for _, attempt := range []int{5, 6, 100} {
		if _, err := pol.Delay(attempt, nil); !errors.Is(err, errs.ErrState) {
			t.Errorf("attempt %d: %v", attempt, err)
		}
	}
	bad := Policy{Base: 0, Max: time.Second, Factor: 2, MaxAttempts: 3}
	if _, err := bad.Delay(1, nil); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("invalid policy: %v", err)
	}
	if _, err := bad.Delay(9, nil); !errors.Is(err, errs.ErrInvalid) {
		t.Fatalf("invalid policy is checked before the attempt window: %v", err)
	}
}
