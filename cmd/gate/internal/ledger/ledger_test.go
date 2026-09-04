package ledger

import (
	"errors"
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func clock(s string) func() time.Time { return func() time.Time { return at(s) } }

func auth(head string) Authorization {
	return Authorization{
		Action: "act_1", Run: "run_1", Repo: "itsHabib/workbench", Number: 42,
		Head: head, Outcome: "would_merge", At: at("2026-08-20T10:00:00Z"),
	}
}

// TestReceiptClassifiesOnTheHeadNotOnTheMerge is the discrimination the whole
// receipt rests on. "The PR merged" is not the question; "the PR merged at the
// head this authorization covered" is. A merge at some other head happened
// outside this authorization, and recording it as a clean discharge would
// launder the gap the receipt exists to expose.
func TestReceiptClassifiesOnTheHeadNotOnTheMerge(t *testing.T) {
	cases := []struct {
		name    string
		landing Landing
		why     string
		want    string
		wantErr error
	}{
		{
			name:    "merged at the authorized head",
			landing: Landing{Merged: true, State: "MERGED", HeadSHA: "abc123", MergeCommit: "m1", Actor: "itsHabib", MergedAt: at("2026-08-20T11:00:00Z")},
			want:    OutcomeMerged,
		},
		{
			name:    "merged at a different head",
			landing: Landing{Merged: true, State: "MERGED", HeadSHA: "def456", MergeCommit: "m2", Actor: "itsHabib", MergedAt: at("2026-08-20T11:00:00Z")},
			want:    OutcomeSuperseded,
		},
		{
			name:    "closed without merging",
			landing: Landing{State: "CLOSED", HeadSHA: "abc123"},
			want:    OutcomeAbandoned,
		},
		{
			name:    "still open, merge attempt reported",
			landing: Landing{State: "OPEN", HeadSHA: "abc123"},
			why:     "gh pr merge refused: head moved",
			want:    OutcomeFailed,
		},
		{
			name:    "still open, nothing reported",
			landing: Landing{State: "OPEN", HeadSHA: "abc123"},
			wantErr: ErrNotLanded,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := NewReceipt(auth("abc123"), c.landing, sourceForTest, c.why, clock("2026-08-20T12:00:00Z"))
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if r.Outcome != c.want {
				t.Fatalf("outcome = %q, want %q", r.Outcome, c.want)
			}
			if c.want == OutcomeSuperseded && r.LandedHead != "def456" {
				t.Fatalf("a superseded landing must name what actually landed, got %q", r.LandedHead)
			}
			if c.want == OutcomeMerged && r.LandedHead != "" {
				t.Fatalf("a clean discharge names no divergent head, got %q", r.LandedHead)
			}
		})
	}
}

const sourceForTest = "executor"

// TestReceiptTakesItsClockFromThePlatform pins the independence that makes a
// receipt worth more than an executor's own log line: the merge time, the merge
// commit, and the actor come from GitHub, so the record is not the actor
// attesting to its own behavior.
func TestReceiptTakesItsClockFromThePlatform(t *testing.T) {
	landing := Landing{
		Merged: true, State: "MERGED", HeadSHA: "abc123",
		MergeCommit: "9f9f9f", Actor: "itsHabib", MergedAt: at("2026-08-20T11:22:33Z"),
	}
	r, err := NewReceipt(auth("abc123"), landing, sourceForTest, "", clock("2026-08-20T12:00:00Z"))
	if err != nil {
		t.Fatal(err)
	}
	if r.MergedAt != "2026-08-20T11:22:33Z" {
		t.Fatalf("merged_at = %q, want GitHub's clock, not gate's", r.MergedAt)
	}
	if r.RecordedAt != "2026-08-20T12:00:00Z" {
		t.Fatalf("recorded_at = %q, want gate's own clock", r.RecordedAt)
	}
	if r.MergedAt == r.RecordedAt {
		t.Fatal("the two clocks must stay distinct — that is the whole point of reading one back")
	}
	if r.MergeCommit != "9f9f9f" || r.Actor != "itsHabib" {
		t.Fatalf("merge commit and actor must come from the platform: %+v", r)
	}
	if r.Action != "act_1" {
		t.Fatalf("a receipt must name the action it discharges, got %q", r.Action)
	}
}

// TestReceiptRefusesANonAuthorization pins that only an authorization can be
// discharged: a block has no landing to record.
func TestReceiptRefusesANonAuthorization(t *testing.T) {
	a := auth("abc123")
	a.Outcome = "blocked"
	_, err := NewReceipt(a, Landing{Merged: true, HeadSHA: "abc123"}, sourceForTest, "", time.Now)
	if !errors.Is(err, ErrNotAuthorizing) {
		t.Fatalf("err = %v, want ErrNotAuthorizing", err)
	}
}
