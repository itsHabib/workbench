package flat

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
	AdmitRefusals    int            `json:"admit_refusals"`
	Nudges           int            `json:"nudges"`
	Takeovers        int            `json:"resource_takeovers"`
	LocksBroken      int            `json:"locks_broken"`
	Refusals         map[string]int `json:"refusals,omitempty"`
	Wakes            int            `json:"wakes"`           // seats resumed for notes
	Routed           int            `json:"routed"`          // requests fanned out to at least one seat
	RuledByRouted    int            `json:"ruled_by_routed"` // rulings made by a seat the index picked
}

// Stats computes the scorecard. threshold is the unclaimed kill line.
func (s *State) Stats(threshold time.Duration, base string) (*Stats, error) {
	st := &Stats{ByNeeds: map[string]int{}, Refusals: map[string]int{}}
	events, err := s.Events()
	if err != nil {
		return nil, err
	}
	asked := map[string]time.Time{}
	claimed := map[string]time.Time{}
	ruled := map[string]time.Time{}
	routed := map[string]map[string]bool{}
	for _, e := range events {
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
			routed[e.Request] = set
		case "ask":
			asked[e.Request] = e.At
			st.Requests++
			st.ByNeeds[e.Tier]++
		case "claim":
			if _, ok := claimed[e.Request]; !ok {
				claimed[e.Request] = e.At
			}
		case "rule":
			ruled[e.Request] = e.At
			st.Ruled++
			if routed[e.Request][e.By] {
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
	st.OperatorRequests += st.ByNeeds[TierOperator]
	now := Now()
	var claimTimes, ruleTimes []float64
	for id, at := range asked {
		if c, ok := claimed[id]; ok {
			d := c.Sub(at)
			claimTimes = append(claimTimes, d.Seconds())
			if d > threshold {
				st.UnclaimedOver++
			}
		} else if now.Sub(at) > threshold {
			st.UnclaimedOver++
		}
		if r, ok := ruled[id]; ok {
			ruleTimes = append(ruleTimes, r.Sub(at).Seconds())
		} else {
			st.Open++
		}
	}
	st.ClaimP50Seconds, st.ClaimMaxSeconds = p50max(claimTimes)
	st.RuleP50Seconds, st.RuleMaxSeconds = p50max(ruleTimes)

	b, err := s.Board(BoardOptions{Base: base})
	if err != nil {
		return nil, err
	}
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
	return st, nil
}

func p50max(xs []float64) (p50, max float64) {
	if len(xs) == 0 {
		return 0, 0
	}
	sort.Float64s(xs)
	return xs[len(xs)/2], xs[len(xs)-1]
}
