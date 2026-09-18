package gym

import (
	"sort"
	"time"
)

// Time as an input. A projection says, from what the team has measured
// about itself, when the work will finish and whether one more seat would
// move that. It is the same arithmetic whether a rule or a model reads it.

// Projection is the team's own estimate of its finish.
type Projection struct {
	DeadlineS    float64 `json:"deadline_s,omitempty"`
	MedianUnitS  float64 `json:"median_unit_s"`
	UnitsPerMin  float64 `json:"units_per_min"`
	Remaining    int     `json:"remaining_units"`
	Ready        int     `json:"ready_units"`
	Active       int     `json:"active_seats"`
	CriticalS    float64 `json:"critical_path_s"`
	FinishS      float64 `json:"projected_finish_s"`
	FinishPlus1S float64 `json:"projected_finish_with_one_more_seat_s"`
	SeatGainS    float64 `json:"seat_gain_s"`
	SlipS        float64 `json:"slip_s,omitempty"`
	Recommend    string  `json:"recommend"` // hold | add_seat | retire_seat | escalate
	Why          string  `json:"why"`
	SampleUnits  int     `json:"sample_units"`
}

// project computes the projection from the work list and the live seats.
// Unit times come from the units already done (claim to done is not
// recorded, so a unit's time is the gap between consecutive done stamps of
// its finisher, floored at one minute); with fewer than two samples a
// default of five minutes stands in and the projection says so.
func project(rows []workRow, active, idle int, elapsed, deadline time.Duration) Projection {
	p := Projection{Active: active, MedianUnitS: 300}
	if deadline > 0 {
		p.DeadlineS = deadline.Seconds()
	}
	byID := map[string]workRow{}
	done := map[string]bool{}
	var doneAt []time.Time
	perSeat := map[string][]time.Time{}
	for _, w := range rows {
		byID[w.ID] = w
		if w.State == "done" {
			done[w.ID] = true
			if !w.DoneAt.IsZero() {
				doneAt = append(doneAt, w.DoneAt)
				perSeat[w.DoneBy] = append(perSeat[w.DoneBy], w.DoneAt)
			}
		}
	}
	var samples []float64
	for _, ts := range perSeat {
		sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
		for i := 1; i < len(ts); i++ {
			if d := ts[i].Sub(ts[i-1]).Seconds(); d >= 60 {
				samples = append(samples, d)
			}
		}
	}
	p.SampleUnits = len(samples)
	if len(samples) >= 2 {
		sort.Float64s(samples)
		p.MedianUnitS = samples[len(samples)/2]
	}
	if len(doneAt) >= 2 && elapsed > 0 {
		p.UnitsPerMin = float64(len(doneAt)) / elapsed.Minutes()
	}
	// remaining, ready, and the critical path in units through the wait graph
	depth := map[string]int{}
	var depthOf func(id string, seen map[string]bool) int
	depthOf = func(id string, seen map[string]bool) int {
		if done[id] {
			return 0
		}
		if d, ok := depth[id]; ok {
			return d
		}
		if seen[id] {
			return 1
		}
		seen[id] = true
		best := 0
		for _, dep := range byID[id].After {
			if d := depthOf(dep, seen); d > best {
				best = d
			}
		}
		depth[id] = best + 1
		return best + 1
	}
	longest := 0
	for _, w := range rows {
		if w.State == "done" || w.Team {
			continue
		}
		p.Remaining++
		if w.State == "open" {
			ready := true
			for _, dep := range w.After {
				if !done[dep] {
					ready = false
				}
			}
			if ready {
				p.Ready++
			}
		}
		if d := depthOf(w.ID, map[string]bool{}); d > longest {
			longest = d
		}
	}
	p.CriticalS = float64(longest) * p.MedianUnitS
	finish := func(seats int) float64 {
		if seats < 1 {
			seats = 1
		}
		work := float64(p.Remaining) * p.MedianUnitS / float64(seats)
		if p.CriticalS > work {
			return p.CriticalS
		}
		return work
	}
	p.FinishS = elapsed.Seconds() + finish(active)
	p.FinishPlus1S = elapsed.Seconds() + finish(active+1)
	p.SeatGainS = p.FinishS - p.FinishPlus1S
	switch {
	case p.Remaining == 0:
		p.Recommend, p.Why = "hold", "nothing remains"
	case deadline > 0 && p.FinishS > deadline.Seconds():
		p.SlipS = p.FinishS - deadline.Seconds()
		if p.Ready <= active-idle || p.SeatGainS < 30 {
			p.Recommend, p.Why = "escalate", "the deadline slips and one more seat would not fix it: the critical path or the ready set is the limit"
		} else if p.FinishPlus1S <= deadline.Seconds() {
			p.Recommend, p.Why = "add_seat", "the deadline slips now and one more seat brings the finish inside it"
		} else {
			p.Recommend, p.Why = "add_seat", "the deadline slips; one more seat helps but may not be enough"
		}
	case idle > p.Ready && p.Remaining < active:
		p.Recommend, p.Why = "retire_seat", "more idle seats than claimable units, and fewer units than seats remain"
	default:
		p.Recommend, p.Why = "hold", "on track, or no deadline to be late for"
	}
	return p
}
