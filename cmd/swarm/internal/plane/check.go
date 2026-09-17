package plane

import (
	"fmt"
	"sort"
	"time"
)

// Violation is one broken invariant, with the history lines that show it.
type Violation struct {
	Rule   string `json:"rule"`
	Item   string `json:"item"`
	Detail string `json:"detail"`
	Seq    int64  `json:"seq"`
}

// Report is what the checker concluded from a history.
type Report struct {
	Items      int            `json:"items"`
	Accepted   int            `json:"accepted"`
	Claims     int            `json:"claims"`
	Refusals   map[string]int `json:"refusals"`
	Replays    int            `json:"replays"`
	Unfinished []string       `json:"unfinished,omitempty"`
	Violations []Violation    `json:"violations,omitempty"`
}

// OK reports whether the history is safe and, when complete is demanded,
// finished.
func (r *Report) OK() bool { return len(r.Violations) == 0 && len(r.Unfinished) == 0 }

// lease is the checker's own model of who holds an item, rebuilt from the
// history alone. It shares no code with the transitions it judges.
type lease struct {
	epoch    int64
	owner    string
	until    time.Time
	done     bool
	doneSeq  int64
	maxEpoch int64
}

// Check replays a history and reports every violation of the contract:
//
//	accepted-once     an item is committed at most once
//	current-lease     an accepted commit carries the item's newest epoch and its owner
//	epochs-grow       a granted epoch is greater than every epoch granted before it
//	no-claim-after    nothing is granted on an item after it is committed
//	no-steal          nothing is granted while another owner's lease is unexpired
//	replay-faithful   an idempotent replay repeats the original epoch
//
// mustFinish lists the kinds whose every item has to end committed.
func Check(history []Event, mustFinish ...string) *Report {
	rep := &Report{Refusals: map[string]int{}}
	items := map[string]*lease{}
	kinds := map[string]string{}
	firstGrant := map[string]int64{} // idem -> epoch
	sort.SliceStable(history, func(i, j int) bool { return history[i].Seq < history[j].Seq })
	bad := func(rule string, e Event, format string, args ...any) {
		rep.Violations = append(rep.Violations, Violation{Rule: rule, Item: key(e.Kind, e.ID), Seq: e.Seq, Detail: fmt.Sprintf(format, args...)})
	}
	for _, e := range history {
		k := key(e.Kind, e.ID)
		l := items[k]
		if l == nil {
			l = &lease{}
			items[k] = l
			kinds[k] = e.Kind
		}
		if !e.OK {
			rep.Refusals[e.Op+":"+e.Code]++
			continue
		}
		if e.Replay {
			rep.Replays++
			if e.Op == "claim" && e.Idem != "" && firstGrant[e.Idem] != e.Epoch {
				bad("replay-faithful", e, "replayed claim %q returned epoch %d, original was %d", e.Idem, e.Epoch, firstGrant[e.Idem])
			}
			continue
		}
		switch e.Op {
		case "claim":
			rep.Claims++
			if l.done {
				bad("no-claim-after", e, "granted epoch %d to %s after commit at seq %d", e.Epoch, e.By, l.doneSeq)
			}
			if e.Epoch <= l.maxEpoch {
				bad("epochs-grow", e, "granted epoch %d, already granted up to %d", e.Epoch, l.maxEpoch)
			}
			if l.owner != "" && l.owner != e.By && e.At.Before(l.until) {
				bad("no-steal", e, "granted to %s at %s while %s held it until %s", e.By, e.At.Format(time.RFC3339Nano), l.owner, l.until.Format(time.RFC3339Nano))
			}
			l.epoch, l.owner, l.until = e.Epoch, e.By, e.Until
			if e.Epoch > l.maxEpoch {
				l.maxEpoch = e.Epoch
			}
			if e.Idem != "" {
				firstGrant[e.Idem] = e.Epoch
			}
		case "renew":
			if e.Epoch == l.epoch && e.By == l.owner {
				l.until = e.Until
			}
		case "release":
			if e.Epoch == l.epoch && e.By == l.owner {
				l.owner, l.until = "", time.Time{}
			}
		case "commit":
			if l.done {
				bad("accepted-once", e, "second accepted commit by %s (epoch %d); first at seq %d", e.By, e.Epoch, l.doneSeq)
			}
			if e.Epoch != l.epoch || e.By != l.owner {
				bad("current-lease", e, "accepted commit by %s under epoch %d; the current lease is %s epoch %d", e.By, e.Epoch, l.owner, l.epoch)
			}
			if !l.done {
				rep.Accepted++
			}
			l.done, l.doneSeq = true, e.Seq
			l.owner, l.until = "", time.Time{}
		}
	}
	rep.Items = len(items)
	want := map[string]bool{}
	for _, k := range mustFinish {
		want[k] = true
	}
	for k, l := range items {
		if want[kinds[k]] && !l.done {
			rep.Unfinished = append(rep.Unfinished, k)
		}
	}
	sort.Strings(rep.Unfinished)
	return rep
}
