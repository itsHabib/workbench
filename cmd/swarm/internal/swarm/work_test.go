package swarm

import (
	"testing"
	"time"
)

// The team's own breakdown lives on the plane: one holder at a time, a seat
// that lost its lease cannot land the unit, and waiting wakes on progress.
func TestWorkClaimFencesAndIdleWakes(t *testing.T) {
	s := onPlane(t)
	if _, err := s.WorkAdd("parse", "parser", []string{"a.go"}, "alice", "", false, ""); err != nil {
		t.Fatal(err)
	}
	_, err := s.WorkAdd("parse", "again", nil, "bob", "", false, "")
	refused(t, err, "exists")
	if _, err := s.WorkClaim("parse", "bob", time.Minute); err != nil {
		t.Fatal(err)
	}
	_, err = s.WorkClaim("parse", "alice", time.Minute)
	refused(t, err, "held_by_other")
	_, err = s.WorkDone("parse", "alice", "x", "")
	refused(t, err, "not_yours")
	if _, err := s.WorkDrop("parse", "bob"); err != nil {
		t.Fatal(err)
	}
	if got := s.Idle("alice", time.Second); got == "" {
		t.Fatal("open work did not wake an idle seat")
	}
	w, err := s.WorkClaim("parse", "alice", time.Minute)
	if err != nil || w.Epoch < 2 {
		t.Fatalf("reclaim: %+v %v", w, err)
	}
	_, err = s.WorkDone("parse", "bob", "late", "")
	refused(t, err, "not_yours")
	if _, err := s.WorkDone("parse", "alice", "done", "abc123"); err != nil {
		t.Fatal(err)
	}
	if got := s.Idle("bob", time.Second); got != "every unit of work is done" {
		t.Fatalf("idle said %q", got)
	}
	ws, _ := s.WorkList()
	if len(ws) != 1 || ws[0].State != "done" || ws[0].DoneBy != "alice" || ws[0].Head != "abc123" || ws[0].Result != "done" {
		t.Fatalf("%+v", ws)
	}
}

// A seat may hold two units, not three; the third claim is refused so one
// seat cannot empty the list while the others idle.
func TestWorkWIPLimit(t *testing.T) {
	s := onPlane(t)
	for _, id := range []string{"a", "b", "c"} {
		if _, err := s.WorkAdd(id, id, nil, "x", "", false, ""); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b"} {
		if _, err := s.WorkClaim(id, "p1", time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.WorkClaim("c", "p1", time.Minute)
	refused(t, err, "wip_full")
	if _, err := s.WorkClaim("a", "p1", time.Minute); err != nil {
		t.Fatal("renewing a held unit counted against the limit:", err)
	}
	if _, err := s.WorkClaim("c", "p2", time.Minute); err != nil {
		t.Fatal(err)
	}
}

// Two founders who each add a team for the same directory get one team.
func TestTeamUnitsCannotOverlap(t *testing.T) {
	s := onPlane(t)
	_, err := s.WorkAdd("shop-team", "shop", nil, "p1", "", true, "build shop/")
	refused(t, err, "bad_work")
	if _, err := s.WorkAdd("shop-team", "shop", []string{"shop"}, "p1", "", true, "build shop/"); err != nil {
		t.Fatal(err)
	}
	_, err = s.WorkAdd("shop-subsystem", "shop again", []string{"shop/"}, "p2", "", true, "build shop/")
	refused(t, err, "overlaps")
	_, err = s.WorkAdd("shop-cart", "part of shop", []string{"shop/cart"}, "p2", "", true, "cart only")
	refused(t, err, "overlaps")
	if _, err := s.WorkAdd("sched-team", "sched", []string{"sched"}, "p2", "", true, "build sched/"); err != nil {
		t.Fatal(err)
	}
}
