package standup

import (
	"encoding/json"
	"fmt"
	"io"
)

// Draft contains only editable plan fields. Confirmation, identity and execution
// receipts cannot be supplied through the planning interface.
type Draft struct {
	Roles     []Role     `json:"roles"`
	Cards     []Card     `json:"cards"`
	Decisions []Decision `json:"decisions"`
	Deferred  []Deferred `json:"deferred"`
	Next      string     `json:"next,omitempty"`
}

// DecodeDraft rejects misspellings and protected fields, including nested ones.
func DecodeDraft(in io.Reader) (Draft, error) {
	var d Draft
	dec := json.NewDecoder(io.LimitReader(in, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil {
		return d, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return d, fmt.Errorf("draft must contain exactly one JSON object")
	}
	return d, nil
}

// UpdateDraft replaces the editable fields against the version last shown. The
// caller holds the store lock. A changed draft loses its confirmation; a record
// with execution history must be carried to a new agenda instead.
func UpdateDraft(r *Record, d Draft, expected string) error {
	if expected == "" || expected != r.PlanDigest() {
		return refuse("plan changed; show the current record and use its plan_digest")
	}
	if len(r.Applied) > 0 {
		return refuse("record has execution history; use new --from against a fresh agenda")
	}
	next := *r
	next.Roles, next.Cards, next.Decisions, next.Deferred, next.Next = d.Roles, d.Cards, d.Decisions, d.Deferred, d.Next
	normalizePlan(&next)
	if next.PlanDigest() != r.PlanDigest() {
		next.Confirm = nil
	}
	if err := next.Validate(); err != nil {
		return err
	}
	*r = next
	return nil
}

func normalizePlan(r *Record) {
	if r.Roles == nil {
		r.Roles = []Role{}
	}
	if r.Cards == nil {
		r.Cards = []Card{}
	}
	if r.Decisions == nil {
		r.Decisions = []Decision{}
	}
	if r.Deferred == nil {
		r.Deferred = []Deferred{}
	}
}
