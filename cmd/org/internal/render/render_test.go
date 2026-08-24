package render

import (
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/contracts/org"
)

var now = time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)

func state() org.RoleState {
	return org.RoleState{
		Tenant: "acme", Role: "lead:platform", Phase: org.PhaseHeld,
		Seq: 8, Tip: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Holder: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		Terms:  org.Terms{Scope: []string{"github:acme/api"}, Tier: "T2", Supervisors: []string{"human:op"}},
		Held: []org.Assignment{
			{Work: "github:acme/api#88"}, {Work: "jira:PROJ-412"},
		},
	}
}

type blobs map[string]string

func (b blobs) Blob(digest string) ([]byte, bool, error) {
	body, ok := b[digest]
	return []byte(body), ok, nil
}

// TestBootLeadsWithObligation proves the dangling claim renders before
// anything shedable: it is the one line a skimming session must not miss.
func TestBootLeadsWithObligation(t *testing.T) {
	s := state()
	s.Dangling = "github:acme/api#88"
	b, err := NewBoot(s, nil, blobs{}, now)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	text := b.Text(2048)
	if !strings.Contains(text, "OBLIGATION: a predecessor's claim on github:acme/api#88") {
		t.Fatalf("boot text lacks the obligation line:\n%s", text)
	}
	tiny := b.Text(220)
	if !strings.Contains(tiny, "OBLIGATION") {
		t.Fatalf("shedding dropped the obligation line:\n%s", tiny)
	}
}

// TestBootShedsDepthNotHeadline proves the budget is met by shrinking the last
// word and the held list, never the headline or charter.
func TestBootShedsDepthNotHeadline(t *testing.T) {
	records := []org.Record{{
		Seq: 8, Kind: org.KindCheckpoint, KindClass: org.ClassAdvisory,
		At: "2026-08-24T11:00:00Z", BodyDigest: "sha256:cccc",
	}}
	store := blobs{"sha256:cccc": strings.Repeat("a long conclusion. ", 200)}
	b, err := NewBoot(state(), records, store, now)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	full := b.Text(1 << 20)
	if !strings.Contains(full, "a long conclusion.") {
		t.Fatalf("full boot lacks the last word body:\n%s", full)
	}
	small := b.Text(400)
	if len(small) > 400 {
		t.Fatalf("budget 400 produced %d bytes", len(small))
	}
	for _, keep := range []string{"# baton boot", "charter: tier T2", "phase: held"} {
		if !strings.Contains(small, keep) {
			t.Fatalf("shedding dropped %q:\n%s", keep, small)
		}
	}
}

// TestBootRendersErasedBody proves an erased blob is said out loud rather than
// silently missing.
func TestBootRendersErasedBody(t *testing.T) {
	records := []org.Record{{
		Seq: 8, Kind: org.KindCheckpoint, KindClass: org.ClassAdvisory,
		At: "2026-08-24T11:00:00Z", BodyDigest: "sha256:gone",
	}}
	b, err := NewBoot(state(), records, blobs{}, now)
	if err != nil {
		t.Fatalf("boot: %v", err)
	}
	if !strings.Contains(b.Text(2048), "(body erased)") {
		t.Fatalf("erased body not rendered:\n%s", b.Text(2048))
	}
}

// TestLateDerivation proves liveness is derived from the declared deadline,
// and that a garbled deadline reads as dead rather than immortal.
func TestLateDerivation(t *testing.T) {
	cases := []struct {
		nextDue string
		want    bool
	}{
		{"", false},
		{now.Add(time.Hour).Format(time.RFC3339), false},
		{now.Add(-time.Hour).Format(time.RFC3339), true},
		{"not-a-timestamp", true},
	}
	for _, c := range cases {
		if got := late(c.nextDue, now); got != c.want {
			t.Fatalf("late(%q) = %v, want %v", c.nextDue, got, c.want)
		}
	}
}

// TestRowCountsObligations proves the board's OPEN column counts intents,
// escalations and a dangling claim together — everything a human must look at.
func TestRowCountsObligations(t *testing.T) {
	s := state()
	s.Dangling = "github:acme/api#88"
	s.OpenIntents = []string{"effect-1"}
	s.OpenEscalations = []string{"sha256:dddd"}
	row := NewRow(s, now)
	if row.Open != 3 {
		t.Fatalf("open = %d, want 3", row.Open)
	}
	if !strings.Contains(Board([]Row{row}), "lead:platform") {
		t.Fatalf("board lacks the role row")
	}
}
