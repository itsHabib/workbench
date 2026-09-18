package errs

import (
	"errors"
	"fmt"
	"testing"
)

func TestHiddenSentinelsDistinct(t *testing.T) {
	all := []error{ErrInvalid, ErrNotFound, ErrConflict, ErrState, ErrExpired, ErrCycle}
	for i, a := range all {
		if a == nil {
			t.Fatalf("sentinel %d is nil", i)
		}
		for j, b := range all {
			if i != j && errors.Is(a, b) {
				t.Fatalf("sentinel %d matches %d", i, j)
			}
		}
	}
}

func TestHiddenCodes(t *testing.T) {
	cases := map[string]error{
		"OK": nil, "INVALID": ErrInvalid, "NOT_FOUND": ErrNotFound, "CONFLICT": ErrConflict,
		"STATE": ErrState, "EXPIRED": ErrExpired, "CYCLE": ErrCycle, "INTERNAL": errors.New("boom"),
	}
	for want, err := range cases {
		if got := Code(err); got != want {
			t.Errorf("Code(%v) = %q, want %q", err, got, want)
		}
	}
}

func TestHiddenWrappedKeepsCode(t *testing.T) {
	err := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ErrExpired))
	if !errors.Is(err, ErrExpired) || Code(err) != "EXPIRED" {
		t.Fatalf("%v -> %s", err, Code(err))
	}
}

func TestHiddenFirstMatchWins(t *testing.T) {
	both := errors.Join(ErrCycle, ErrNotFound)
	if got := Code(both); got != "NOT_FOUND" {
		t.Fatalf("got %s", got)
	}
	both = errors.Join(ErrState, ErrInvalid)
	if got := Code(both); got != "INVALID" {
		t.Fatalf("got %s", got)
	}
}
