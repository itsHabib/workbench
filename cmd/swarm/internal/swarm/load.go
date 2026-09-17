package swarm

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// SeatLoad is the queue one address is carrying. The number an operator
// feels when a lead "gets too many messages" is wait: how long things sit
// before that address gets to them. When wait grows, the answer in a flat
// fleet is another wakeable peer at the same tier, not a layer above.
type SeatLoad struct {
	Seat          string  `json:"seat"`
	PendingNotes  int     `json:"pending_notes"`   // in the inbox, not yet delivered
	OpenRequests  int     `json:"open_requests"`   // routed here and still open
	OldestOpenMin float64 `json:"oldest_open_min"` // age of the oldest of those
	ClaimAvgMin   float64 `json:"claim_avg_min"`   // mean wait over the window, requests this seat claimed
	ClaimMaxMin   float64 `json:"claim_max_min"`
	RuledInWindow int     `json:"ruled_in_window"`
	claims        int     // claims counted into ClaimAvgMin
}

// Load reports every address's queue over the last window, operator
// included, worst wait first.
func (s *State) Load(window time.Duration) ([]SeatLoad, error) {
	events, err := s.Events()
	if err != nil {
		return nil, err
	}
	now := Now()
	since := now.Add(-window)
	by := map[string]*SeatLoad{}
	get := func(seat string) *SeatLoad {
		l := by[seat]
		if l == nil {
			l = &SeatLoad{Seat: seat}
			by[seat] = l
		}
		return l
	}
	_, routedTo := s.foldLoadEvents(events, since, get)
	reqs, err := s.Requests(true)
	if err != nil {
		return nil, err
	}
	for _, r := range reqs {
		targets := routedTo[r.ID]
		if r.Needs == TierOperator {
			targets = []string{"operator"}
		}
		age := now.Sub(r.At).Minutes()
		for _, seat := range targets {
			l := get(seat)
			l.OpenRequests++
			if age > l.OldestOpenMin {
				l.OldestOpenMin = age
			}
		}
	}
	for seat := range by {
		notes, _ := s.Inbox(seat, false)
		by[seat].PendingNotes = len(notes)
	}
	out := make([]SeatLoad, 0, len(by))
	for _, l := range by {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].OldestOpenMin != out[j].OldestOpenMin {
			return out[i].OldestOpenMin > out[j].OldestOpenMin
		}
		return out[i].Seat < out[j].Seat
	})
	return out, nil
}

// foldLoadEvents walks the event log once, collecting ask times, routing
// targets, and each seat's claim waits and rulings inside the window.
func (s *State) foldLoadEvents(events []Event, since time.Time, get func(string) *SeatLoad) (map[string]time.Time, map[string][]string) {
	asked := map[string]time.Time{}
	routedTo := map[string][]string{}
	for _, e := range events {
		switch e.Kind {
		case "ask":
			asked[e.Request] = e.At
		case "route":
			for _, seat := range strings.Split(e.Detail, ",") {
				if seat != "" {
					routedTo[e.Request] = append(routedTo[e.Request], seat)
				}
			}
		case "claim":
			if at, ok := asked[e.Request]; ok && e.At.After(since) {
				l := get(e.By)
				l.ClaimAvgMin, l.ClaimMaxMin = accumulate(l, e.At.Sub(at).Minutes())
			}
		case "rule":
			if e.At.After(since) {
				get(e.By).RuledInWindow++
			}
		}
	}
	return asked, routedTo
}

// accumulate keeps a running mean and max without storing every sample.
func accumulate(l *SeatLoad, wait float64) (avg, hi float64) {
	l.claims++
	n := float64(l.claims)
	avg = l.ClaimAvgMin + (wait-l.ClaimAvgMin)/n
	hi = l.ClaimMaxMin
	if wait > hi {
		hi = wait
	}
	return avg, hi
}

// LoadText renders the load table.
func LoadText(ls []SeatLoad) string {
	if len(ls) == 0 {
		return "no load recorded"
	}
	var sb strings.Builder
	sb.WriteString("seat                 open  oldest  notes  claim avg/max (min)  ruled\n")
	for _, l := range ls {
		fmt.Fprintf(&sb, "%-20s %4d  %5.1fm  %5d  %6.1f / %-6.1f       %d\n", trunc(l.Seat, 20), l.OpenRequests, l.OldestOpenMin, l.PendingNotes, l.ClaimAvgMin, l.ClaimMaxMin, l.RuledInWindow)
	}
	return strings.TrimRight(sb.String(), "\n")
}
