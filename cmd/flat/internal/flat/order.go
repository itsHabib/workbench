package flat

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Sequence is the landing order the ledger implies: the theme map the lead
// used to write by hand.
type Sequence struct {
	Order    []string `json:"order"`
	Pending  []string `json:"pending,omitempty"`  // branches not landed yet
	Unruled  []string `json:"unruled,omitempty"`  // contended pairs with no order ruling
	Rulings  []string `json:"rulings,omitempty"`  // decision ids that fixed an order
	Conflict string   `json:"conflict,omitempty"` // an order cycle, if the ledger contradicts itself
}

// Text renders the sequence for a consolidator.
func (q *Sequence) Text() string {
	var sb strings.Builder
	sb.WriteString("landing order:\n")
	for i, b := range q.Order {
		fmt.Fprintf(&sb, "  %d. %s\n", i+1, b)
	}
	if len(q.Pending) > 0 {
		fmt.Fprintf(&sb, "not landed yet: %s\n", strings.Join(q.Pending, ", "))
	}
	if len(q.Unruled) > 0 {
		fmt.Fprintf(&sb, "contended with no order ruling (ordered by start time): %s\n", strings.Join(q.Unruled, "; "))
	}
	if q.Conflict != "" {
		fmt.Fprintf(&sb, "CONFLICT: %s\n", q.Conflict)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// Order sorts landed branches by the ledger's order rulings, breaking ties
// by each branch's first commit time.
func (s *State) Order(opts BoardOptions) (*Sequence, error) {
	b, err := s.Board(opts)
	if err != nil {
		return nil, err
	}
	effective, err := s.Effective()
	if err != nil {
		return nil, err
	}
	q := &Sequence{}
	started := map[string]time.Time{}
	landed := map[string]bool{}
	for _, r := range b.Rows {
		if r.State != "landed" {
			q.Pending = append(q.Pending, r.Branch)
			continue
		}
		landed[r.Branch] = true
		started[r.Branch] = s.firstCommitAt(r, b)
	}
	before := map[string]map[string]bool{} // before[x][y]: x lands before y
	for _, d := range effective {
		if len(d.Order) < 2 {
			continue
		}
		q.Rulings = append(q.Rulings, d.ID)
		for i := 0; i+1 < len(d.Order); i++ {
			x, y := d.Order[i], d.Order[i+1]
			if before[x] == nil {
				before[x] = map[string]bool{}
			}
			before[x][y] = true
		}
	}
	for _, c := range b.Contended {
		var ls []string
		for _, br := range c.Branches {
			if landed[br] {
				ls = append(ls, br)
			}
		}
		for i := 0; i < len(ls); i++ {
			for j := i + 1; j < len(ls); j++ {
				if !before[ls[i]][ls[j]] && !before[ls[j]][ls[i]] {
					q.Unruled = append(q.Unruled, ls[i]+" vs "+ls[j]+" on "+c.File)
				}
			}
		}
	}
	names := make([]string, 0, len(landed))
	for n := range landed {
		names = append(names, n)
	}
	sort.Strings(names)
	sort.SliceStable(names, func(i, j int) bool { return started[names[i]].Before(started[names[j]]) })
	q.Order, q.Conflict = topo(names, before)
	return q, nil
}

// topo orders names respecting before, taking the earliest-started
// available branch each step; a cycle is reported, not silently broken.
func topo(names []string, before map[string]map[string]bool) ([]string, string) {
	done := map[string]bool{}
	var out []string
	for len(out) < len(names) {
		picked := ""
		for _, n := range names {
			if done[n] {
				continue
			}
			blocked := false
			for x, ys := range before {
				if ys[n] && !done[x] && contains(names, x) {
					blocked = true
				}
			}
			if !blocked {
				picked = n
				break
			}
		}
		if picked == "" {
			var left []string
			for _, n := range names {
				if !done[n] {
					left = append(left, n)
				}
			}
			return out, "order rulings form a cycle among " + strings.Join(left, ", ")
		}
		done[picked] = true
		out = append(out, picked)
	}
	return out, ""
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// firstCommitAt is when a branch's own work began: its earliest commit
// that no branch it is built on already had.
func (s *State) firstCommitAt(r Row, b *Board) time.Time {
	args := append([]string{"log", "--reverse", "--format=%ct", r.Tip, "--not", b.BaseSHA}, s.inherited(r.Branch, r.Tip, b.tips)...)
	out, err := Git(s.Repo, args...)
	if err != nil || out == "" {
		return r.TipAt
	}
	var ts int64
	fmt.Sscanf(strings.SplitN(out, "\n", 2)[0], "%d", &ts)
	return time.Unix(ts, 0).UTC()
}
