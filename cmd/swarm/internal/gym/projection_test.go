package gym

import (
	"testing"
	"time"
)

func rows(n int, chain bool) []workRow {
	var out []workRow
	for i := 0; i < n; i++ {
		w := workRow{ID: string(rune('a' + i)), State: "open"}
		if chain && i > 0 {
			w.After = []string{string(rune('a' + i - 1))}
		}
		out = append(out, w)
	}
	return out
}

// With no samples the projection assumes five minutes a unit. Independent
// units divide across seats; a chain does not, and says so.
func TestProjectionSeatGainAndCriticalPath(t *testing.T) {
	p := project(rows(8, false), 2, 0, 0, 15*time.Minute)
	if p.Remaining != 8 || p.Ready != 8 || p.FinishS != 8*300/2 || p.SeatGainS != 8*300/2-8*300/3 {
		t.Fatalf("%+v", p)
	}
	if p.Recommend != "add_seat" {
		t.Fatalf("independent units past the deadline: %s (%s)", p.Recommend, p.Why)
	}
	p = project(rows(8, true), 2, 1, 0, 20*time.Minute)
	if p.CriticalS != 8*300 || p.Ready != 1 || p.SeatGainS != 0 || p.Recommend != "escalate" {
		t.Fatalf("chain: %+v", p)
	}
	p = project(rows(8, false), 2, 0, 0, 2*time.Hour)
	if p.Recommend != "hold" {
		t.Fatalf("generous deadline: %s", p.Recommend)
	}
	p = project(rows(1, false), 3, 2, 0, 0)
	if p.Recommend != "retire_seat" {
		t.Fatalf("more seats than units: %s", p.Recommend)
	}
}

// Measured pace replaces the default once two units have finished in
// sequence by the same seat.
func TestProjectionUsesMeasuredPace(t *testing.T) {
	t0 := time.Unix(1000, 0)
	rs := rows(6, false)
	for i := 0; i < 3; i++ {
		rs[i].State, rs[i].DoneBy, rs[i].DoneAt = "done", "p1", t0.Add(time.Duration(i)*2*time.Minute)
	}
	p := project(rs, 1, 0, 6*time.Minute, 0)
	if p.MedianUnitS != 120 || p.SampleUnits != 2 || p.Remaining != 3 {
		t.Fatalf("%+v", p)
	}
	if p.FinishS != 360+3*120 {
		t.Fatalf("finish %v", p.FinishS)
	}
}
