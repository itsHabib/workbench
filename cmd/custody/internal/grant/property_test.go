package grant

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// Attenuation is the law Derive claims: a child grant narrows every axis of its
// parent, and the narrowing lives in the signed envelope. The example tests pin
// the named cases; this generator asserts the ALGEBRAIC invariants over drawn
// action-sets and TTLs — a successful derive is always a subset, never outlasts
// its parent, and is always depth-1; a violating input always refuses with the
// matching coded error, following Derive's own actions-before-ttl precedence.

var deriveAlphabet = []string{"read", "comment", "write", "merge", "label"}

// drawActions draws a non-empty action set from the fixed alphabet.
func drawActions(rt *rapid.T, label string) []string {
	var out []string
	for i, a := range deriveAlphabet {
		if rapid.Bool().Draw(rt, fmt.Sprintf("%s_%d", label, i)) {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		out = append(out, deriveAlphabet[0])
	}
	return out
}

func TestDeriveAttenuationProperty(t *testing.T) {
	mint := fixedClock(time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	rapid.Check(t, func(rt *rapid.T) {
		s := newStore(t)
		parentActions := drawActions(rt, "parent")
		parentHours := rapid.IntRange(2, 48).Draw(rt, "parentTTLh")
		parent, parentTok, err := s.Mint("tracker", parentActions, time.Duration(parentHours)*time.Hour, "operator", mint)
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}

		childActions := drawActions(rt, "child")
		childHours := rapid.IntRange(1, 72).Draw(rt, "childTTLh")
		boundSource := rapid.SampledFrom([]string{"", "10.0.0.5", "192.168.1.9"}).Draw(rt, "bound")
		child, childTok, derr := s.Derive(parentTok, childActions, time.Duration(childHours)*time.Hour, boundSource, "agent", mint)

		subsetOK := subset(childActions, parentActions)
		expiryOK := childHours <= parentHours
		if subsetOK && expiryOK {
			requireDeriveInvariants(t, s, parent, child, childTok, childActions, derr, mint)
			return
		}
		requireDeriveRefusal(t, subsetOK, derr)
	})
}

// requireDeriveInvariants asserts every property a successful derive must hold:
// subset actions, expiry no later than the parent's, a recorded parent link, a
// clean validation round-trip, and an unconditional depth-1 cap (no grandchild).
func requireDeriveInvariants(t *testing.T, s *Store, parent, child Grant, childTok string, childActions []string, derr error, now func() time.Time) {
	t.Helper()
	if derr != nil {
		t.Fatalf("derive within bounds should succeed: %v", derr)
	}
	if !subset(child.Actions, parent.Actions) {
		t.Fatalf("child actions %v not a subset of parent %v", child.Actions, parent.Actions)
	}
	if child.Expiry().After(parent.Expiry()) {
		t.Fatalf("child expiry %s after parent %s", child.Expiry(), parent.Expiry())
	}
	if child.Parent != parent.ID {
		t.Fatalf("child.Parent = %q, want %q", child.Parent, parent.ID)
	}
	got, err := s.Validate(childTok, "tracker", now)
	if err != nil {
		t.Fatalf("validate derived child: %v", err)
	}
	for _, a := range childActions {
		if !got.Covers(a) {
			t.Fatalf("child should cover derived action %q: %v", a, got.Actions)
		}
	}
	if _, _, err := s.Derive(childTok, childActions, time.Hour, "", "agent", now); !errors.Is(err, ErrChainDepth) {
		t.Fatalf("deriving from a child must refuse ErrChainDepth, got %v", err)
	}
}

// requireDeriveRefusal asserts a violating input refuses with the coded error
// Derive's precedence dictates: actions are checked before ttl, so a non-subset
// refuses on actions regardless of expiry.
func requireDeriveRefusal(t *testing.T, subsetOK bool, derr error) {
	t.Helper()
	if !subsetOK {
		if !errors.Is(derr, ErrAttenuationActions) {
			t.Fatalf("action superset must refuse ErrAttenuationActions, got %v", derr)
		}
		return
	}
	if !errors.Is(derr, ErrAttenuationTTL) {
		t.Fatalf("ttl past parent must refuse ErrAttenuationTTL, got %v", derr)
	}
}
