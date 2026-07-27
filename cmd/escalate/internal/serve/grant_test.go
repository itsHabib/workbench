package serve

import (
	"errors"
	"strings"
	"testing"

	"github.com/itsHabib/workbench/contracts/escalation"
)

// TestGrantForEscalation pins the join: the grant of the parked run whose
// escalation id matches, an error for an id absent from the inbox, and an error
// for an unreadable feed.
func TestGrantForEscalation(t *testing.T) {
	feed := []byte(`{"parked":[
		{"escalation":"esc_a","grant":"grt_a"},
		{"escalation":"esc_b","grant":"grt_b"}
	]}`)

	got, err := grantForEscalation(feed, "esc_b")
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if got != "grt_b" {
		t.Fatalf("grant = %q, want grt_b", got)
	}

	_, err = grantForEscalation(feed, "esc_missing")
	if !errors.Is(err, ErrNotParked) {
		t.Fatalf("an id absent from the inbox must be ErrNotParked, got %v", err)
	}
	if !strings.Contains(err.Error(), "esc_missing") {
		t.Fatalf("error should name the missing id, got %v", err)
	}

	if _, err := grantForEscalation([]byte("not json"), "esc_a"); err == nil {
		t.Fatalf("an unreadable feed must error")
	}
}

// TestSlackWhoPrefersHandle pins the identity precedence: a handle wins over the
// display name, which wins over the opaque user id.
func TestSlackWhoPrefersHandle(t *testing.T) {
	cases := []struct {
		username, name, id, want string
	}{
		{"michael", "Michael H", "U1", "@michael"},
		{"", "Michael H", "U1", "@Michael H"},
		{"", "", "U1", "U1"},
		{"", "", "", ""},
	}
	for _, tc := range cases {
		if got := slackWho(tc.username, tc.name, tc.id); got != tc.want {
			t.Fatalf("slackWho(%q,%q,%q) = %q, want %q", tc.username, tc.name, tc.id, got, tc.want)
		}
	}
}

// TestVerdictFor pins the action_id → decision map and that an unknown button
// is rejected, never defaulted.
func TestVerdictFor(t *testing.T) {
	if v, err := verdictFor(actionApprove); err != nil || v != escalation.DecisionPass {
		t.Fatalf("approve → %q,%v, want pass", v, err)
	}
	if v, err := verdictFor(actionBlock); err != nil || v != escalation.DecisionBlock {
		t.Fatalf("block → %q,%v, want block", v, err)
	}
	if _, err := verdictFor("whatever"); err == nil {
		t.Fatalf("an unknown action_id must error")
	}
}
