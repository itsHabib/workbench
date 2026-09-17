package plane

import (
	"errors"
	"sort"
	"time"
)

// Data is the whole store, as the file and memory backends hold it. The
// transitions below are pure functions of Data and a clock; a backend's
// only job is to run one of them atomically and durably.
type Data struct {
	Items   map[string]*Item   `json:"items"`   // kind/id
	Order   []string           `json:"order"`   // creation order, for ClaimNext
	Claims  map[string]Grant   `json:"claims"`  // idempotency: claim key -> grant
	Commits map[string]Receipt `json:"commits"` // idempotency: commit key -> receipt
	Seq     int64              `json:"seq"`
	Log     []Event            `json:"log"`
}

// NewData returns an empty store.
func NewData() *Data {
	return &Data{Items: map[string]*Item{}, Claims: map[string]Grant{}, Commits: map[string]Receipt{}}
}

// Broken switches off one safety check at a time. It exists so the fault
// harness can prove its checker catches a store that is actually wrong; a
// checker that has never failed anything has not been tested.
type Broken struct {
	NoEpochCheck   bool // commit accepts any epoch
	NoDoneCheck    bool // commit accepts after a commit
	NoPendingCheck bool // claim grants items already done
	NoIdempotency  bool // retries act again
	EpochReuse     bool // release resets the epoch
}

func key(kind, id string) string { return kind + "/" + id }

func (d *Data) log(now time.Time, e Event) {
	d.Seq++
	e.Seq, e.At = d.Seq, now
	d.Log = append(d.Log, e)
}

func (d *Data) put(now time.Time, it Item) {
	k := key(it.Kind, it.ID)
	if _, ok := d.Items[k]; ok {
		return
	}
	it.State, it.Version = Pending, 1
	d.Items[k] = &it
	d.Order = append(d.Order, k)
	d.log(now, Event{Op: "put", Kind: it.Kind, ID: it.ID, OK: true, By: it.Parent})
}

func claimable(it *Item, now time.Time, inc string, b Broken) error {
	if it.State == Done && !b.NoPendingCheck {
		return ErrDone
	}
	if it.Owner != "" && it.Owner != inc && now.Before(it.Until) {
		return ErrHeld
	}
	return nil
}

func (d *Data) claim(now time.Time, kind, id, inc string, ttl time.Duration, idem string, b Broken) (Grant, error) {
	if g, ok := d.Claims[idem]; ok && idem != "" && !b.NoIdempotency {
		d.log(now, Event{Op: "claim", Kind: g.Kind, ID: g.ID, By: inc, Epoch: g.Epoch, OK: true, Idem: idem, Replay: true})
		return g, nil
	}
	it, ok := d.Items[key(kind, id)]
	if !ok {
		return Grant{}, ErrNotFound
	}
	if err := claimable(it, now, inc, b); err != nil {
		d.log(now, Event{Op: "claim", Kind: kind, ID: id, By: inc, OK: false, Code: code(err), Idem: idem})
		return Grant{}, err
	}
	it.Epoch++
	it.Owner, it.Until = inc, now.Add(ttl)
	it.Version++
	g := Grant{Kind: kind, ID: id, Epoch: it.Epoch, Owner: inc, Until: it.Until, Payload: it.Payload}
	if idem != "" {
		d.Claims[idem] = g
	}
	d.log(now, Event{Op: "claim", Kind: kind, ID: id, By: inc, Epoch: g.Epoch, OK: true, Idem: idem, Until: g.Until})
	return g, nil
}

func (d *Data) claimNext(now time.Time, kind, inc string, ttl time.Duration, idem string, b Broken) (Grant, error) {
	if g, ok := d.Claims[idem]; ok && idem != "" && !b.NoIdempotency {
		d.log(now, Event{Op: "claim", Kind: g.Kind, ID: g.ID, By: inc, Epoch: g.Epoch, OK: true, Idem: idem, Replay: true})
		return g, nil
	}
	for _, k := range d.Order {
		it := d.Items[k]
		if it.Kind != kind || claimable(it, now, inc, b) != nil {
			continue
		}
		return d.claim(now, kind, it.ID, inc, ttl, idem, b)
	}
	return Grant{}, ErrNone
}

// holds checks that g is the item's current, unexpired lease.
func holds(it *Item, g Grant, now time.Time) error {
	switch {
	case it.Epoch != g.Epoch || it.Owner != g.Owner:
		return ErrFenced
	case !now.Before(it.Until):
		return ErrExpired
	}
	return nil
}

func (d *Data) renew(now time.Time, g Grant, ttl time.Duration) (Grant, error) {
	it, ok := d.Items[key(g.Kind, g.ID)]
	if !ok {
		return Grant{}, ErrNotFound
	}
	if it.State == Done {
		return Grant{}, ErrDone
	}
	if err := holds(it, g, now); err != nil {
		d.log(now, Event{Op: "renew", Kind: g.Kind, ID: g.ID, By: g.Owner, Epoch: g.Epoch, OK: false, Code: code(err)})
		return Grant{}, err
	}
	it.Until = now.Add(ttl)
	it.Version++
	g.Until = it.Until
	d.log(now, Event{Op: "renew", Kind: g.Kind, ID: g.ID, By: g.Owner, Epoch: g.Epoch, OK: true, Until: g.Until})
	return g, nil
}

func (d *Data) release(now time.Time, g Grant, b Broken) error {
	it, ok := d.Items[key(g.Kind, g.ID)]
	if !ok {
		return ErrNotFound
	}
	if it.Epoch != g.Epoch || it.Owner != g.Owner {
		d.log(now, Event{Op: "release", Kind: g.Kind, ID: g.ID, By: g.Owner, Epoch: g.Epoch, OK: false, Code: "fenced"})
		return ErrFenced
	}
	it.Owner, it.Until = "", time.Time{}
	if b.EpochReuse {
		it.Epoch = 0
	}
	it.Version++
	d.log(now, Event{Op: "release", Kind: g.Kind, ID: g.ID, By: g.Owner, Epoch: g.Epoch, OK: true})
	return nil
}

func (d *Data) commit(now time.Time, g Grant, result, idem string, emit []Item, b Broken) (Receipt, error) {
	if r, ok := d.Commits[idem]; ok && idem != "" && !b.NoIdempotency {
		d.log(now, Event{Op: "commit", Kind: r.Kind, ID: r.ID, By: g.Owner, Epoch: r.Epoch, OK: r.Accepted, Idem: idem, Replay: true, Result: r.Result})
		if !r.Accepted {
			return r, ErrDone
		}
		return r, nil
	}
	it, ok := d.Items[key(g.Kind, g.ID)]
	if !ok {
		return Receipt{}, ErrNotFound
	}
	// Done is checked in the same step as the acceptance: a fencing token
	// read separately from "is it still pending" admits a double acceptance.
	if it.State == Done && !b.NoDoneCheck {
		r := Receipt{Kind: g.Kind, ID: g.ID, Accepted: false, Epoch: it.DoneEpoch, Winner: it.DoneBy, Result: it.Result}
		if idem != "" {
			d.Commits[idem] = r
		}
		d.log(now, Event{Op: "commit", Kind: g.Kind, ID: g.ID, By: g.Owner, Epoch: g.Epoch, OK: false, Code: "done", Idem: idem})
		return r, ErrDone
	}
	if !b.NoEpochCheck && (it.Epoch != g.Epoch || it.Owner != g.Owner) {
		d.log(now, Event{Op: "commit", Kind: g.Kind, ID: g.ID, By: g.Owner, Epoch: g.Epoch, OK: false, Code: "fenced", Idem: idem})
		return Receipt{}, ErrFenced
	}
	it.State, it.Result, it.DoneBy, it.DoneEpoch = Done, result, g.Owner, g.Epoch
	it.Owner, it.Until = "", time.Time{}
	it.Version++
	d.log(now, Event{Op: "commit", Kind: g.Kind, ID: g.ID, By: g.Owner, Epoch: g.Epoch, OK: true, Idem: idem, Result: result})
	for _, e := range emit {
		e.Parent = key(g.Kind, g.ID)
		d.put(now, e)
	}
	r := Receipt{Kind: g.Kind, ID: g.ID, Accepted: true, Epoch: g.Epoch, Winner: g.Owner, Result: result, Emitted: len(emit)}
	if idem != "" {
		d.Commits[idem] = r
	}
	return r, nil
}

func (d *Data) list(kind string) []Item {
	var out []Item
	for _, k := range d.Order {
		if it := d.Items[k]; it.Kind == kind {
			out = append(out, *it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func code(err error) string {
	switch {
	case errors.Is(err, ErrHeld):
		return "held"
	case errors.Is(err, ErrFenced):
		return "fenced"
	case errors.Is(err, ErrDone):
		return "done"
	case errors.Is(err, ErrExpired):
		return "expired"
	case errors.Is(err, ErrNone):
		return "none"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	}
	return "error"
}
