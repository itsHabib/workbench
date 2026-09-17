package swarm

import (
	"testing"
	"time"
)

// The team's own breakdown lives on the plane: one holder at a time, a seat
// that lost its lease cannot land the unit, and waiting wakes on progress.
func TestWorkClaimFencesAndIdleWakes(t *testing.T) {
	s := onPlane(t)
	if _, err := s.WorkAdd("parse", "parser", []string{"a.go"}, "alice", ""); err != nil {
		t.Fatal(err)
	}
	_, err := s.WorkAdd("parse", "again", nil, "bob", "")
	refused(t, err, "exists")
	if _, err := s.WorkClaim("parse", "bob", time.Minute); err != nil {
		t.Fatal(err)
	}
	_, err = s.WorkClaim("parse", "alice", time.Minute)
	refused(t, err, "held_by_other")
	_, err = s.WorkDone("parse", "alice", "x")
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
	_, err = s.WorkDone("parse", "bob", "late")
	refused(t, err, "not_yours")
	if _, err := s.WorkDone("parse", "alice", "done"); err != nil {
		t.Fatal(err)
	}
	if got := s.Idle("bob", time.Second); got != "every unit of work is done" {
		t.Fatalf("idle said %q", got)
	}
	ws, _ := s.WorkList()
	if len(ws) != 1 || ws[0].State != "done" || ws[0].DoneBy != "alice" {
		t.Fatalf("%+v", ws)
	}
}
