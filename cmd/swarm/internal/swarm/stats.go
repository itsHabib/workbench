package swarm

import (
	"sort"
	"strings"
	"time"
)

// Stats is what the kill conditions score. Every number comes from the
// event log, the ledger and the board, never from anyone's report.
type Stats struct {
	Requests         int            `json:"requests"`
	ByNeeds          map[string]int `json:"by_needs"`
	Escalations      int            `json:"escalations"`
	Ruled            int            `json:"ruled"`
	Open             int            `json:"open"`
	ClaimP50Seconds  float64        `json:"claim_p50_s"`
	ClaimMaxSeconds  float64        `json:"claim_max_s"`
	RuleP50Seconds   float64        `json:"rule_p50_s"`
	RuleMaxSeconds   float64        `json:"rule_max_s"`
	UnclaimedOver    int            `json:"unclaimed_over"` // requests whose first claim came later than the threshold, or never
	OperatorRequests int            `json:"operator_requests"`
	OperatorRulings  int            `json:"operator_rulings"`
	PinViolations    int            `json:"pin_violations"`
	PinInvalid       int            `json:"pin_invalid"`
	Contended        int            `json:"contended"`
	ContendedUnruled int            `json:"contended_unruled"`
	Landed           int            `json:"landed"`
	Blocked          int            `json:"blocked"`
	Working          int            `json:"working"`
	Silent           int            `json:"silent"`
	Red              int            `json:"red"` // landed but verification failed
	AdmitRefusals    int            `json:"admit_refusals"`
	Nudges           int            `json:"nudges"`
	Takeovers        int            `json:"resource_takeovers"`
	LocksBroken      int            `json:"locks_broken"`
	Refusals         map[string]int `json:"refusals,omitempty"`
	Wakes            int            `json:"wakes"`           // seats resumed for notes
	Routed           int            `json:"routed"`          // requests fanned out to at least one seat
	RuledByRouted    int            `json:"ruled_by_routed"` // rulings made by a seat the index picked
}

// tally is the per-request timeline the event log yields.
type tally struct {
	asked, claimed, ruled map[string]time.Time
	routed                map[string]map[string]bool
}

func newTally() *tally {
	return &tally{asked: map[string]time.Time{}, claimed: map[string]time.Time{}, ruled: map[string]time.Time{}, routed: map[string]map[string]bool{}}
}

// Stats computes the scorecard. threshold is the unclaimed kill line.
func (s *State) Stats(threshold time.Duration, base string) (*Stats, error) {
	st := &Stats{ByNeeds: map[string]int{}, Refusals: map[string]int{}}
	events, err := s.Events()
	if err != nil {
		return nil, err
	}
	t := newTally()
	for _, e := range events {
		st.absorb(e, t)
	}
	st.OperatorRequests += st.ByNeeds[TierOperator]
	st.latencies(t, threshold)
	b, err := s.Board(BoardOptions{Base: base})
	if err != nil {
		return nil, err
	}
	st.fromBoard(b)
	return st, nil
}

func (st *Stats) absorb(e Event, t *tally) {
	switch e.Kind {
	case "route":
		set := map[string]bool{}
		for _, seat := range strings.Split(e.Detail, ",") {
			if seat != "" {
				set[seat] = true
			}
		}
		if len(set) > 0 {
			st.Routed++
		}
		t.routed[e.Request] = set
	case "ask":
		t.asked[e.Request] = e.At
		st.Requests++
		st.ByNeeds[e.Tier]++
	case "claim":
		if _, ok := t.claimed[e.Request]; !ok {
			t.claimed[e.Request] = e.At
		}
	case "rule":
		t.ruled[e.Request] = e.At
		st.Ruled++
		if t.routed[e.Request][e.By] {
			st.RuledByRouted++
		}
		if e.Tier == TierOperator {
			st.OperatorRulings++
		}
	case "escalate":
		st.Escalations++
		if e.Tier == TierOperator {
			st.OperatorRequests++
		}
	case "refuse_admit":
		st.AdmitRefusals++
	case "nudge":
		st.Nudges++
	case "wake":
		st.Wakes++
	case "take_over":
		st.Takeovers++
	case "lock_broken":
		st.LocksBroken++
	case "refusal":
		st.Refusals[e.Detail]++
	}
}

func (st *Stats) latencies(t *tally, threshold time.Duration) {
	now := Now()
	var claimTimes, ruleTimes []float64
	for id, at := range t.asked {
		c, claimed := t.claimed[id]
		switch {
		case claimed:
			claimTimes = append(claimTimes, c.Sub(at).Seconds())
			if c.Sub(at) > threshold {
				st.UnclaimedOver++
			}
		case now.Sub(at) > threshold:
			st.UnclaimedOver++
		}
		if r, ok := t.ruled[id]; ok {
			ruleTimes = append(ruleTimes, r.Sub(at).Seconds())
			continue
		}
		st.Open++
	}
	st.ClaimP50Seconds, st.ClaimMaxSeconds = p50max(claimTimes)
	st.RuleP50Seconds, st.RuleMaxSeconds = p50max(ruleTimes)
}

func (st *Stats) fromBoard(b *Board) {
	for _, r := range b.Rows {
		switch r.State {
		case "landed":
			st.Landed++
		case "blocked":
			st.Blocked++
		case "working":
			st.Working++
		case "silent":
			st.Silent++
		case "red":
			st.Red++
		case "pin_violation":
			st.PinViolations++
		case "pin_invalid":
			st.PinInvalid++
		}
	}
	st.Contended = len(b.Contended)
	for _, c := range b.Contended {
		if !c.Ruled {
			st.ContendedUnruled++
		}
	}
}

func p50max(xs []float64) (p50, hi float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	sort.Float64s(xs)
	return xs[len(xs)/2], xs[len(xs)-1]
}
