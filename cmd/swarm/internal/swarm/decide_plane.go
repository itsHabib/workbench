package swarm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/swarm/internal/plane"
)

// When SWARM_STORE names a store, requests, rulings and notes live on the
// plane instead of in files beside the repository. That is what lets seats
// on different machines, or in microVM clones that share nothing, ask each
// other questions and hear the answers.
//
//	a request   is an item of kind "request"; its payload is the question
//	a claim     is the plane's lease on it, with the plane's epoch
//	a ruling    is the commit of that item; the result is the decision
//	a note      is an item of kind "note:<seat>", emitted inside the commit
//	            that caused it, so a ruling and its notification are one step
//	escalation  commits the request as escalated and emits its successor at
//	            the higher tier in the same step; wait follows the chain
//
// Tier and tiebreak are policy and stay here, above the store. They are
// made race-free by holding the ledger lease while they are evaluated.

const (
	kindRequest = "request"
	kindLock    = "lock"
)

func remote() bool { return os.Getenv("SWARM_STORE") != "" }

func noteKind(seat string) string { return "note:" + seat }

// planeResult is what a committed request holds: a decision, or where the
// question went.
type planeResult struct {
	Decision    *Decision `json:"decision,omitempty"`
	EscalatedTo string    `json:"escalated_to,omitempty"`
}

func requestFromItem(it plane.Item) Request {
	var r Request
	_ = json.Unmarshal([]byte(it.Payload), &r)
	r.ID = it.ID
	r.Epoch = int(it.Epoch)
	r.Status = "open"
	switch {
	case it.State == plane.Done:
		r.Status = "ruled"
		var res planeResult
		if json.Unmarshal([]byte(it.Result), &res) == nil {
			if res.Decision != nil {
				r.Decision, r.RuledAt = res.Decision.ID, res.Decision.At
			}
			if res.EscalatedTo != "" {
				r.Status, r.Decision = "escalated", res.EscalatedTo
			}
		}
	case it.Owner != "" && Now().Before(it.Until):
		r.Status = "claimed"
		r.Claim = &Claim{Holder: seatOf(it.Owner), Epoch: int(it.Epoch), Until: it.Until}
	}
	return r
}

// seatOf strips an incarnation suffix: "seat@uuid" is seat.
func seatOf(owner string) string {
	seat, _, _ := strings.Cut(owner, "@")
	return seat
}

func noteItem(seat, kind, text string) plane.Item {
	n := Note{ID: NewID("note"), At: Now(), Seat: seat, Kind: kind, Text: text}
	data, _ := json.Marshal(n)
	return plane.Item{Kind: noteKind(seat), ID: n.ID, Payload: string(data)}
}

func (s *State) askP(r *Request) (*Request, error) {
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	payload, _ := json.Marshal(r)
	if err := st.Put(context.Background(), plane.Item{Kind: kindRequest, ID: r.ID, Payload: string(payload)}); err != nil {
		return nil, err
	}
	s.appendEvent(Event{Kind: "ask", Request: r.ID, By: r.From, Seat: r.From, Tier: r.Needs, Detail: strings.Join(r.Scope, ",")})
	s.route(r)
	return r, nil
}

func (s *State) requestsP(openOnly bool) ([]Request, error) {
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	items, err := st.List(context.Background(), kindRequest)
	if err != nil {
		return nil, err
	}
	var out []Request
	for _, it := range items {
		r := requestFromItem(it)
		if r.Proactive || (openOnly && it.State == plane.Done) {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

func planeRefusal(err error, id string, it plane.Item) error {
	switch {
	case errors.Is(err, plane.ErrHeld):
		return refuse("claimed_by_other", "%s is claimed by %s until %s", id, seatOf(it.Owner), it.Until.Format(time.RFC3339))
	case errors.Is(err, plane.ErrDone):
		return refuse("already_ruled", "%s was already ruled", id)
	case errors.Is(err, plane.ErrFenced):
		return refuse("claim_fenced", "your claim on %s is not the current lease; it changed hands", id)
	case errors.Is(err, plane.ErrExpired):
		return refuse("claim_expired", "your claim on %s expired; claim again", id)
	case errors.Is(err, plane.ErrNotFound):
		return refuse("no_such_request", "%s", id)
	}
	return err
}

func (s *State) claimRequestP(id, holder string, ttl time.Duration) (*Request, error) {
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	ctx := context.Background()
	it, err := st.Get(ctx, kindRequest, id)
	if err != nil {
		return nil, planeRefusal(err, id, it)
	}
	r := requestFromItem(it)
	if Level(s.TierOf(holder)) < Level(r.Needs) {
		return nil, refuse("tier_too_low", "%s needs %s; %s is %s. escalate or leave it", id, r.Needs, holder, s.TierOf(holder))
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	g, err := st.Claim(ctx, kindRequest, id, incarnation(holder), ttl, "")
	if err != nil {
		return nil, planeRefusal(err, id, it)
	}
	r.Status, r.Epoch = "claimed", int(g.Epoch)
	r.Claim = &Claim{Holder: holder, Epoch: int(g.Epoch), At: Now(), Until: g.Until}
	s.appendEvent(Event{Kind: "claim", Request: id, By: holder, Epoch: int(g.Epoch)})
	return &r, nil
}

// grantFor returns the lease a ruling or escalation acts under: the
// caller's live claim when it names one, otherwise a fresh claim.
func grantFor(ctx context.Context, st plane.Store, it plane.Item, id, by string, epoch int) (plane.Grant, error) {
	owner := incarnation(by)
	if epoch != 0 {
		// An explicit epoch is a fencing token. It never acquires.
		switch {
		case it.Owner != owner || it.Epoch != int64(epoch):
			return plane.Grant{}, refuse("claim_fenced", "epoch %d is not your live claim on %s; it changed hands or was never yours", epoch, id)
		case !Now().Before(it.Until):
			return plane.Grant{}, refuse("claim_expired", "your claim on %s expired at %s; claim again", id, it.Until.Format(time.RFC3339))
		}
		return plane.Grant{Kind: kindRequest, ID: id, Epoch: it.Epoch, Owner: owner, Until: it.Until}, nil
	}
	if it.Owner == owner && Now().Before(it.Until) {
		return plane.Grant{Kind: kindRequest, ID: id, Epoch: it.Epoch, Owner: owner, Until: it.Until}, nil
	}
	g, err := st.Claim(ctx, kindRequest, id, owner, time.Minute, "")
	if err != nil {
		return plane.Grant{}, planeRefusal(err, id, it)
	}
	return g, nil
}

// withLedger holds the ledger lease while fn evaluates and commits a
// ruling, so two rulings on overlapping scope cannot both pass the
// tiebreak. The lease is short and lapses on its own if the holder dies.
func withLedger(ctx context.Context, st plane.Store, by string, fn func() error) error {
	_ = st.Put(ctx, plane.Item{Kind: kindLock, ID: "ledger"})
	deadline := time.Now().Add(10 * time.Second)
	for {
		g, err := st.Claim(ctx, kindLock, "ledger", incarnation(by)+"#"+NewID("l"), 5*time.Second, "")
		if err == nil {
			defer func() { _ = st.Release(ctx, g) }()
			return fn()
		}
		if !errors.Is(err, plane.ErrHeld) || time.Now().After(deadline) {
			return fmt.Errorf("ledger busy: %w", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (s *State) ruleP(id, by string, epoch int, ruling, evidence, supersedes string, order []string) (*Decision, error) {
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	ctx := context.Background()
	it, err := st.Get(ctx, kindRequest, id)
	if err != nil {
		return nil, planeRefusal(err, id, it)
	}
	r := requestFromItem(it)
	if it.State == plane.Done {
		// One request, one ruling. The same ruler asking again hears the
		// original answer; anyone else is told it is settled.
		var res planeResult
		if json.Unmarshal([]byte(it.Result), &res) == nil && res.Decision != nil && res.Decision.By == by {
			return res.Decision, nil
		}
		return nil, refuse("already_ruled", "%s was ruled by %s", id, r.Decision)
	}
	tier := s.TierOf(by)
	if Level(tier) < Level(r.Needs) {
		return nil, refuse("tier_too_low", "%s needs %s; %s is %s. use: swarm escalate %s --to %s --why '...'", id, r.Needs, by, tier, id, r.Needs)
	}
	g, err := grantFor(ctx, st, it, id, by, epoch)
	if err != nil {
		return nil, err
	}
	var d *Decision
	err = withLedger(ctx, st, by, func() error {
		sup, err := s.tiebreak(r.Scope, tier, supersedes)
		if err != nil {
			return err
		}
		d = &Decision{ID: NewID("dec"), At: Now(), Request: id, Scope: r.Scope, Ruling: ruling, By: by, Tier: tier, Evidence: evidence, Supersedes: sup, Order: order}
		result, _ := json.Marshal(planeResult{Decision: d})
		note := noteItem(r.From, "ruling", fmt.Sprintf("ruled %s by %s (%s): %s", id, by, tier, ruling))
		_, err = st.Commit(ctx, g, string(result), fmt.Sprintf("rule:%s:e%d", id, g.Epoch), []plane.Item{note})
		return err
	})
	if err != nil {
		var ref *Refusal
		if errors.As(err, &ref) {
			return nil, err
		}
		return nil, planeRefusal(err, id, it)
	}
	s.appendEvent(Event{Kind: "rule", Request: id, By: by, Tier: tier, Epoch: int(g.Epoch), Detail: d.ID})
	return d, nil
}

func (s *State) escalateP(id, by, to, why string) (*Request, error) {
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	ctx := context.Background()
	it, err := st.Get(ctx, kindRequest, id)
	if err != nil {
		return nil, planeRefusal(err, id, it)
	}
	r := requestFromItem(it)
	if it.State == plane.Done {
		return nil, refuse("already_ruled", "%s was ruled by %s", id, r.Decision)
	}
	if Level(to) <= Level(r.Needs) {
		return nil, refuse("not_an_escalation", "%s already needs %s", id, r.Needs)
	}
	g, err := grantFor(ctx, st, it, id, by, 0)
	if err != nil {
		return nil, err
	}
	// The successor is the same question at the higher tier. It is created
	// in the commit that closes this one, along with the notes that tell the
	// new tier and the asker, so none of the three can exist without the rest.
	next := r
	next.ID, next.Needs, next.Status, next.Claim, next.Decision = id+"~"+to, to, "open", nil, ""
	next.Escalations = append(append([]Escalation{}, r.Escalations...), Escalation{At: Now(), By: by, From: r.Needs, To: to, Why: why})
	payload, _ := json.Marshal(next)
	emit := []plane.Item{{Kind: kindRequest, ID: next.ID, Payload: string(payload)},
		noteItem(r.From, "escalated", fmt.Sprintf("%s was escalated to %s by %s: %s. wait on %s", id, to, by, why, next.ID))}
	for _, seat := range s.tierSeats(to) {
		emit = append(emit, noteItem(seat, "request", fmt.Sprintf("request %s from %s on %s needs %s: %q (escalated by %s: %s)", next.ID, r.From, strings.Join(r.Scope, ","), to, r.Question, by, why)))
	}
	result, _ := json.Marshal(planeResult{EscalatedTo: next.ID})
	if _, err := st.Commit(ctx, g, string(result), "escalate:"+id, emit); err != nil {
		return nil, planeRefusal(err, id, it)
	}
	s.appendEvent(Event{Kind: "escalate", Request: id, By: by, Tier: to, Detail: why})
	return &next, nil
}

const kindConfig = "config"

// tiers reads who outranks whom: from the store when seats share one, so
// every machine agrees, otherwise from the local state.
func (s *State) tiers() Tiers {
	var t Tiers
	if remote() {
		if st, err := s.Plane(); err == nil {
			defer st.Close()
			if items, err := st.List(context.Background(), kindConfig); err == nil {
				for _, it := range items { // the newest tiers item wins
					if strings.HasPrefix(it.ID, "tiers-") {
						_ = json.Unmarshal([]byte(it.Payload), &t)
					}
				}
			}
		}
		if len(t.Operator) > 0 {
			return t
		}
	}
	_ = readJSON(s.path("tiers.json"), &t)
	return t
}

func (s *State) putTiersP(t Tiers) error {
	st, err := s.Plane()
	if err != nil {
		return err
	}
	defer st.Close()
	data, _ := json.Marshal(t)
	return st.Put(context.Background(), plane.Item{Kind: kindConfig, ID: "tiers-" + NewID("v"), Payload: string(data)})
}

// tierSeats names who hears about a request at a tier.
func (s *State) tierSeats(tier string) []string {
	t := s.tiers()
	switch tier {
	case TierOperator:
		return []string{"operator"}
	case TierLead:
		return t.Lead
	}
	return nil
}

func (s *State) decideP(by string, scope []string, ruling, evidence, supersedes string, order []string) (*Decision, error) {
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	ctx := context.Background()
	tier := s.TierOf(by)
	id := NewID("pro")
	payload, _ := json.Marshal(Request{ID: id, At: Now(), From: by, Scope: scope, Question: "(proactive ruling)", Needs: tier, Proactive: true})
	if err := st.Put(ctx, plane.Item{Kind: kindRequest, ID: id, Payload: string(payload)}); err != nil {
		return nil, err
	}
	g, err := st.Claim(ctx, kindRequest, id, incarnation(by), time.Minute, "")
	if err != nil {
		return nil, err
	}
	var d *Decision
	err = withLedger(ctx, st, by, func() error {
		sup, err := s.tiebreak(scope, tier, supersedes)
		if err != nil {
			return err
		}
		d = &Decision{ID: NewID("dec"), At: Now(), Scope: scope, Ruling: ruling, By: by, Tier: tier, Evidence: evidence, Supersedes: sup, Order: order}
		result, _ := json.Marshal(planeResult{Decision: d})
		_, err = st.Commit(ctx, g, string(result), "decide:"+id, nil)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.appendEvent(Event{Kind: "decide", By: by, Tier: tier, Detail: d.ID})
	return d, nil
}

func (s *State) decisionsP() ([]Decision, error) {
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	items, err := st.List(context.Background(), kindRequest)
	if err != nil {
		return nil, err
	}
	var out []Decision
	for _, it := range items {
		var res planeResult
		if it.State == plane.Done && json.Unmarshal([]byte(it.Result), &res) == nil && res.Decision != nil {
			out = append(out, *res.Decision)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.Before(out[j].At)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (s *State) waitP(id string, timeout, every time.Duration) (*Request, *Decision, error) {
	st, err := s.Plane()
	if err != nil {
		return nil, nil, err
	}
	defer st.Close()
	if every <= 0 {
		every = 2 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		it, err := st.Get(context.Background(), kindRequest, id)
		if err != nil {
			return nil, nil, planeRefusal(err, id, it)
		}
		r := requestFromItem(it)
		var res planeResult
		if it.State == plane.Done && json.Unmarshal([]byte(it.Result), &res) == nil {
			if res.Decision != nil {
				return &r, res.Decision, nil
			}
			if res.EscalatedTo != "" {
				id = res.EscalatedTo // the question moved up; keep waiting on its successor
				continue
			}
		}
		if time.Now().After(deadline) {
			return &r, nil, refuse("timeout", "%s not ruled after %s (needs %s)", id, timeout, r.Needs)
		}
		time.Sleep(every)
	}
}

func (s *State) nudgeP(seat, kind, text string) (*Note, error) {
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	it := noteItem(seat, kind, text)
	if err := st.Put(context.Background(), it); err != nil {
		return nil, err
	}
	var n Note
	_ = json.Unmarshal([]byte(it.Payload), &n)
	s.appendEvent(Event{Kind: "nudge", Seat: seat, Detail: kind + ": " + text})
	return &n, nil
}

// inboxP lists a seat's undelivered notes. Consuming one is a claim and a
// commit, so two readers of one inbox (a hook and a wake, or two clones
// sharing a seat name) deliver each note once between them.
func (s *State) inboxP(seat string, consume bool) ([]Note, error) {
	st, err := s.Plane()
	if err != nil {
		return nil, err
	}
	defer st.Close()
	ctx := context.Background()
	items, err := st.List(ctx, noteKind(seat))
	if err != nil {
		return nil, err
	}
	reader := incarnation(seat) + "#" + NewID("r")
	var out []Note
	for _, it := range items {
		if it.State == plane.Done {
			continue
		}
		var n Note
		if json.Unmarshal([]byte(it.Payload), &n) != nil {
			continue
		}
		if consume {
			g, err := st.Claim(ctx, it.Kind, it.ID, reader, time.Minute, "")
			if err != nil {
				continue // another reader has it
			}
			if _, err := st.Commit(ctx, g, "delivered", "deliver:"+it.ID, nil); err != nil {
				continue
			}
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}
