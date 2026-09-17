// Package plane is the coordination store a swarm shares when its peers
// share nothing else: no filesystem, no hostname, no clock they can trust.
//
// Everything a swarm coordinates is an item of some kind: a task to build,
// a request to rule on, a seat to occupy, a resource to hold, an event to
// deliver. An item is pending until exactly one attempt commits it. Two
// operations carry the safety:
//
//	Claim   atomically checks that the item is still pending and unowned
//	        (or its lease expired), and grants a lease under a fresh epoch.
//	Commit  atomically checks that the caller still holds the current epoch
//	        and that nobody committed first, records the result, and creates
//	        any follow-on items (children of a split, events to deliver) in
//	        the same step.
//
// Both are idempotent by key: a retry after a crash or a lost reply returns
// the original answer instead of acting twice. Epochs only grow, including
// across release. Time is the store's, never the caller's. Attempts are
// at-least-once; acceptance is exactly-once.
//
// The store cannot make an external effect (a push, an install, a model
// call) happen once. The protocol is: do the work under a lease, publish
// an immutable result, and let Commit decide whether it is the accepted
// one. Work done under a lost lease is discarded, never merged.
package plane

import (
	"context"
	"errors"
	"time"
)

// Item states.
const (
	Pending = "pending"
	Done    = "done"
)

// Item is one unit of coordination.
type Item struct {
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	State     string    `json:"state"`
	Payload   string    `json:"payload,omitempty"`
	Epoch     int64     `json:"epoch"` // last epoch granted; never decreases
	Owner     string    `json:"owner,omitempty"`
	Until     time.Time `json:"until,omitempty"`
	Version   int64     `json:"version"` // bumps on every change
	Result    string    `json:"result,omitempty"`
	DoneBy    string    `json:"done_by,omitempty"`
	DoneEpoch int64     `json:"done_epoch,omitempty"`
	Parent    string    `json:"parent,omitempty"` // kind/id of the commit that emitted this item
}

// Grant is a lease on an item.
type Grant struct {
	Kind    string    `json:"kind"`
	ID      string    `json:"id"`
	Epoch   int64     `json:"epoch"`
	Owner   string    `json:"owner"`
	Until   time.Time `json:"until"`
	Payload string    `json:"payload,omitempty"`
}

// Receipt is the answer to a commit. Accepted is false when someone else's
// commit is the accepted one; Winner then names it.
type Receipt struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Accepted bool   `json:"accepted"`
	Epoch    int64  `json:"epoch"`
	Winner   string `json:"winner"`
	Result   string `json:"result"`
	Emitted  int    `json:"emitted"`
}

// Typed refusals. A refusal is an answer, not a failure: the caller stops
// and discards its work.
var (
	ErrNone     = errors.New("plane: nothing claimable")
	ErrHeld     = errors.New("plane: held by another incarnation")
	ErrFenced   = errors.New("plane: epoch is not the current lease")
	ErrDone     = errors.New("plane: already committed")
	ErrNotFound = errors.New("plane: no such item")
	ErrExpired  = errors.New("plane: lease expired")
)

// Event is one line of the store's history, written inside the same atomic
// step as the change it records. The checker reads nothing else.
type Event struct {
	Seq    int64     `json:"seq"`
	At     time.Time `json:"at"`
	Op     string    `json:"op"` // put | claim | renew | release | commit
	Kind   string    `json:"kind"`
	ID     string    `json:"id"`
	By     string    `json:"by,omitempty"`
	Epoch  int64     `json:"epoch,omitempty"`
	OK     bool      `json:"ok"`
	Code   string    `json:"code,omitempty"` // refusal code when !OK
	Idem   string    `json:"idem,omitempty"`
	Replay bool      `json:"replay,omitempty"` // an idempotent retry answered from the record
	Result string    `json:"result,omitempty"`
	Until  time.Time `json:"until,omitempty"` // lease end, on claim and renew
}

// Store is the contract. Every method is one atomic step.
type Store interface {
	// Put creates a pending item. Creating an item that exists is a no-op.
	Put(ctx context.Context, it Item) error
	// Claim leases one named item.
	Claim(ctx context.Context, kind, id, incarnation string, ttl time.Duration, idem string) (Grant, error)
	// ClaimNext leases any claimable item of a kind, oldest first.
	ClaimNext(ctx context.Context, kind, incarnation string, ttl time.Duration, idem string) (Grant, error)
	// Renew extends a lease the caller still holds.
	Renew(ctx context.Context, g Grant, ttl time.Duration) (Grant, error)
	// Release gives a lease back. The epoch is retained.
	Release(ctx context.Context, g Grant) error
	// Commit accepts a result under a lease, and creates emit in the same step.
	Commit(ctx context.Context, g Grant, result, idem string, emit []Item) (Receipt, error)
	// Get and List observe. Observations are advisory: act through Claim.
	Get(ctx context.Context, kind, id string) (Item, error)
	List(ctx context.Context, kind string) ([]Item, error)
	// History is the store's own record of every step, in order.
	History(ctx context.Context) ([]Event, error)
	Close() error
}
