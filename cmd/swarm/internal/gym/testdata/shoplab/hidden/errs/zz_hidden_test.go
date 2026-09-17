package errs

import (
	"errors"
	"fmt"
	"testing"
)

func TestHiddenSentinelsDistinct(t *testing.T) {
	all := []error{ErrInvalid, ErrNotFound, ErrConflict, ErrInsufficient, ErrState}
	for i, a := range all {
		if a == nil {
			t.Fatalf("sentinel %d is nil", i)
		}
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("sentinel %d matches sentinel %d", i, j)
			}
		}
	}
}

func TestHiddenCodeTable(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{nil, "OK"}, {ErrInvalid, "INVALID"}, {ErrNotFound, "NOT_FOUND"}, {ErrConflict, "CONFLICT"},
		{ErrInsufficient, "INSUFFICIENT"}, {ErrState, "STATE"},
	} {
		if got := Code(c.err); got != c.want {
			t.Errorf("Code(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestHiddenCodeSeesThroughWrapping(t *testing.T) {
	err := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ErrInsufficient))
	if got := Code(err); got != "INSUFFICIENT" {
		t.Fatalf("got %q", got)
	}
}

func TestHiddenCodeUnknownIsInternal(t *testing.T) {
	if got := Code(errors.New("invalid")); got != "INTERNAL" {
		t.Fatalf("got %q", got)
	}
}

func TestHiddenCodeFirstListedWins(t *testing.T) {
	if got := Code(errors.Join(ErrState, ErrNotFound)); got != "NOT_FOUND" {
		t.Fatalf("got %q", got)
	}
	if got := Code(errors.Join(ErrInsufficient, ErrInvalid, ErrConflict)); got != "INVALID" {
		t.Fatalf("got %q", got)
	}
}
