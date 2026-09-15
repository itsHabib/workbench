package plan

import "github.com/itsHabib/workbench/experiments/language-bakeoff/entries/custom/internal/foundation"

// entry is what the journal says about one declaration id. It is derived
// from backend evidence on every plan; the planner keeps no state of its own.
type entry struct {
	written *foundation.Record // last converge record (kept resources)
	success *receipt           // last completed attempt (work)
	last    *attempt           // last attempt of any outcome (work)
	lastSeq int                // last record of any kind
}

// receipt is a completed attempt: what it ran against and what it left.
type receipt struct {
	start  foundation.Record
	finish foundation.Record
}

type attempt struct {
	start  foundation.Record
	status string // started, finished or failed
	detail string
	endSeq int
}

func history(recs []foundation.Record) map[string]*entry {
	h := map[string]*entry{}
	for _, r := range recs {
		e := h[r.ID]
		if e == nil {
			e = &entry{}
			h[r.ID] = e
		}
		e.lastSeq = r.Seq
		e.add(r)
	}
	return h
}

func (e *entry) add(r foundation.Record) {
	switch r.Op {
	case foundation.OpConverge:
		e.written = &r
	case foundation.OpStart:
		e.last = &attempt{start: r, status: "started"}
	case foundation.OpFinish:
		if e.last == nil || e.last.start.Seq != r.Attempt {
			return
		}
		e.last.status, e.last.endSeq = "finished", r.Seq
		e.success = &receipt{start: e.last.start, finish: r}
	case foundation.OpFail:
		if e.last == nil || e.last.start.Seq != r.Attempt {
			return
		}
		e.last.status, e.last.detail, e.last.endSeq = "failed", r.Detail, r.Seq
	}
}

// unfinished describes a last attempt that did not complete, so a fresh
// caller learns why work is being retried.
func (e *entry) unfinished() []string {
	if e == nil || e.last == nil {
		return nil
	}
	switch e.last.status {
	case "failed":
		return []string{sprintf("attempt #%d failed: %s", e.last.start.Seq, e.last.detail)}
	case "started":
		return []string{sprintf("attempt #%d was interrupted: it started but never recorded an outcome", e.last.start.Seq)}
	}
	return nil
}
