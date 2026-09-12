package ledger

import (
	"testing"
	"time"
)

func window(since, until string) Window {
	return Window{Since: at(since), Until: at(until)}
}

func landed(number int, head, mergedAt string) Landing {
	return Landing{
		Repo: "itsHabib/workbench", Number: number, State: "MERGED", Merged: true,
		HeadSHA: head, MergeCommit: "m" + head, Actor: "itsHabib",
		MergedAt: at(mergedAt), BaseRef: "main",
	}
}

func authorized(action string, number int, head, when string) Authorization {
	return Authorization{
		Action: action, Run: "run_" + action, Repo: "itsHabib/workbench",
		Number: number, Head: head, Outcome: "would_merge", At: at(when),
	}
}

// TestReconcileClassifiesTheThreeCases is the whole point of the sweep: what the
// control covered, what it authorized and nothing landed, and what landed with
// nothing behind it. The third is the number gate could not previously produce
// at any value, including zero.
func TestReconcileClassifiesTheThreeCases(t *testing.T) {
	landings := []Landing{
		landed(10, "aaa", "2026-08-10T10:00:00Z"), // authorized at this head
		landed(11, "bbb", "2026-08-11T10:00:00Z"), // no authorization at all
		landed(12, "ccc", "2026-08-12T10:00:00Z"), // authorized at a DIFFERENT head
	}
	auths := []Authorization{
		authorized("act_10", 10, "aaa", "2026-08-10T09:00:00Z"),
		authorized("act_12", 12, "zzz", "2026-08-12T09:00:00Z"),
		authorized("act_13", 13, "ddd", "2026-08-13T09:00:00Z"), // never landed
	}
	c := Reconcile("itsHabib/workbench", "main",
		window("2026-08-01T00:00:00Z", "2026-08-20T00:00:00Z"),
		at("2026-08-01T00:00:00Z"), "flag",
		landings, auths, map[string]string{}, clock("2026-08-20T00:00:00Z"))

	if len(c.AuthorizedAndLanded) != 1 || c.AuthorizedAndLanded[0].Number != 10 {
		t.Fatalf("authorized_and_landed = %+v, want only #10", c.AuthorizedAndLanded)
	}
	// #12 merged at ccc while only zzz was authorized: joining on the number
	// alone would have called this covered, which is exactly the laundering
	// --match-head-commit exists to prevent.
	got := numbers(c.LandedWithoutAuthorization)
	if len(got) != 2 || got[0] != 11 || got[1] != 12 {
		t.Fatalf("landed_without_authorization = %v, want [11 12]", got)
	}
	// #12's authorization and #13's are both outstanding: neither discharged.
	if never := numbers(c.AuthorizedNeverLanded); len(never) != 2 || never[0] != 12 || never[1] != 13 {
		t.Fatalf("authorized_never_landed = %v, want [12 13]", never)
	}
	if len(c.PreAdoption) != 0 {
		t.Fatalf("nothing predates the boundary here, got %+v", c.PreAdoption)
	}
	if c.Basis != BasisMergedPullRequests {
		t.Fatalf("the artifact must state the boundary of its own claim, got %q", c.Basis)
	}
}

// TestReconcileSeparatesPreAdoptionAtTheBoundary pins the classification that
// keeps the bypass count usable. A merge before the control was in force is
// structurally identical to a bypass; reporting them together would bury a real
// one in history and train the reader to close the surface.
func TestReconcileSeparatesPreAdoptionAtTheBoundary(t *testing.T) {
	boundary := "2026-08-10T00:00:00Z"
	landings := []Landing{
		landed(1, "aaa", "2026-08-09T23:59:59Z"), // one second before
		landed(2, "bbb", boundary),               // exactly at the boundary
		landed(3, "ccc", "2026-08-10T00:00:01Z"), // one second after
	}
	c := Reconcile("itsHabib/workbench", "main",
		window("2026-08-01T00:00:00Z", "2026-08-20T00:00:00Z"),
		at(boundary), "derived", landings, nil, nil, clock("2026-08-20T00:00:00Z"))

	if pre := numbers(c.PreAdoption); len(pre) != 1 || pre[0] != 1 {
		t.Fatalf("pre_adoption = %v, want only #1 — the boundary is inclusive of the instant itself", pre)
	}
	// A merge AT the boundary is in force: the control was operating from that
	// instant, so it is a bypass, not history.
	if bypass := numbers(c.LandedWithoutAuthorization); len(bypass) != 2 || bypass[0] != 2 || bypass[1] != 3 {
		t.Fatalf("landed_without_authorization = %v, want [2 3]", bypass)
	}
	if c.EffectiveFromSource != "derived" {
		t.Fatalf("how the boundary was established must be recorded, got %q", c.EffectiveFromSource)
	}
}

// TestReconcileWithNoBoundaryCallsEverythingUnauthorized pins the honest answer
// for a repo gate has never run on: there is no adoption date to excuse anything,
// so nothing is quietly excused.
func TestReconcileWithNoBoundaryCallsEverythingUnauthorized(t *testing.T) {
	c := Reconcile("itsHabib/workbench", "main",
		window("2026-08-01T00:00:00Z", "2026-08-20T00:00:00Z"),
		time.Time{}, "", []Landing{landed(1, "aaa", "2020-01-01T00:00:00Z")},
		nil, nil, clock("2026-08-20T00:00:00Z"))
	if len(c.PreAdoption) != 0 {
		t.Fatalf("with no boundary nothing may be classified as history, got %+v", c.PreAdoption)
	}
	if len(c.LandedWithoutAuthorization) != 1 {
		t.Fatalf("want the merge reported unauthorized, got %+v", c.LandedWithoutAuthorization)
	}
	if c.EffectiveFrom != "" {
		t.Fatalf("an unknown boundary must stay empty rather than defaulting, got %q", c.EffectiveFrom)
	}
}

// TestFindingsSeparateTheTwoAnomalies pins that the audit's two findings mean
// opposite things — the control ran and nobody wrote back, versus the control
// did not run — and that history is never reported as either.
func TestFindingsSeparateTheTwoAnomalies(t *testing.T) {
	c := Reconcile("itsHabib/workbench", "main",
		window("2026-08-01T00:00:00Z", "2026-08-20T00:00:00Z"),
		at("2026-08-05T00:00:00Z"), "flag",
		[]Landing{
			landed(10, "aaa", "2026-08-10T10:00:00Z"), // authorized, receipted
			landed(11, "bbb", "2026-08-11T10:00:00Z"), // authorized, NOT receipted
			landed(12, "ccc", "2026-08-12T10:00:00Z"), // bypass
			landed(13, "ddd", "2026-08-01T10:00:00Z"), // pre-adoption
		},
		[]Authorization{
			authorized("act_10", 10, "aaa", "2026-08-10T09:00:00Z"),
			authorized("act_11", 11, "bbb", "2026-08-11T09:00:00Z"),
		},
		map[string]string{"act_10": "rct_10"}, clock("2026-08-20T00:00:00Z"))

	f := c.Findings()
	if n := numbers(f.AuthorizationWithoutReceipt); len(n) != 1 || n[0] != 11 {
		t.Fatalf("authorization_without_receipt = %v, want only #11", n)
	}
	if n := numbers(f.MergeWithoutAuthorization); len(n) != 1 || n[0] != 12 {
		t.Fatalf("merge_without_authorization = %v, want only #12 — never the pre-adoption merge", n)
	}
}

func numbers(rows []CoverageRow) []int {
	out := make([]int, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Number)
	}
	return out
}

// TestReconcileCountsOutOfWindowOutstandingAuthorizations pins that the window
// scoping never hides an outstanding authorization: it is excluded from the
// list, because the list is window-scoped like everything else, but counted so
// the reader sees an exclusion rather than an absence.
func TestReconcileCountsOutOfWindowOutstandingAuthorizations(t *testing.T) {
	c := Reconcile("itsHabib/workbench", "main",
		window("2026-08-01T00:00:00Z", "2026-08-20T00:00:00Z"),
		at("2026-07-01T00:00:00Z"), "flag", nil,
		[]Authorization{
			authorized("act_old", 1, "aaa", "2026-06-01T00:00:00Z"), // before the window
			authorized("act_in", 2, "bbb", "2026-08-05T00:00:00Z"),  // inside it
		},
		nil, clock("2026-08-20T00:00:00Z"))

	if n := numbers(c.AuthorizedNeverLanded); len(n) != 1 || n[0] != 2 {
		t.Fatalf("authorized_never_landed = %v, want only the in-window #2", n)
	}
	if c.OutstandingOutsideWindow != 1 {
		t.Fatalf("outstanding_outside_window = %d, want 1 — the exclusion must be visible", c.OutstandingOutsideWindow)
	}
}

// TestOutOfWindowCountExcludesDischargedAuthorizations pins that the
// outstanding count means what it says. An authorization issued and discharged
// before the window is closed; counting it as outstanding would inflate the one
// number whose whole meaning is "never closed".
func TestOutOfWindowCountExcludesDischargedAuthorizations(t *testing.T) {
	auths := []Authorization{
		authorized("act_closed", 1, "aaa", "2026-06-01T00:00:00Z"),
		authorized("act_open", 2, "bbb", "2026-06-02T00:00:00Z"),
	}
	c := Reconcile("itsHabib/workbench", "main",
		window("2026-08-01T00:00:00Z", "2026-08-20T00:00:00Z"),
		at("2026-05-01T00:00:00Z"), "flag", nil, auths,
		map[string]string{"act_closed": "rct_closed"}, clock("2026-08-20T00:00:00Z"))

	if c.OutstandingOutsideWindow != 1 {
		t.Fatalf("outstanding_outside_window = %d, want 1 — a receipted authorization is closed, not outstanding", c.OutstandingOutsideWindow)
	}
}
