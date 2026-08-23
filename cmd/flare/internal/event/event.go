// Package event defines the one value that flows through flare's pipeline:
// a push-worthy fact lifted from a producer's artifact log. Sources decide
// what becomes an Event at all; everything downstream (route, throttle,
// notify) sees only this shape.
package event

import "time"

// Severity orders events so the throttle can be monotone: a strictly worse
// event passes through an open throttle window. Higher is worse.
type Severity int

// Severity bands, worst last.
const (
	SevInfo      Severity = iota // cursor alerts, heartbeats
	SevCancelled                 // a run was cancelled
	SevFailed                    // a run failed
	SevEscalate                  // a producer parked for judgment
	SevBlock                     // a producer blocked outright
)

func (s Severity) String() string {
	switch s {
	case SevBlock:
		return "block"
	case SevEscalate:
		return "escalate"
	case SevFailed:
		return "failed"
	case SevCancelled:
		return "cancelled"
	}
	return "info"
}

// Class separates the two kinds of fact that flow through the pipeline. A
// PAGE is news: it routes to a channel and becomes a notification. An UPDATE is
// not news — it is the current state of a page already delivered, and it is
// APPLIED to that card rather than routed.
//
// The distinction exists because a card that never changes is a lie the moment
// its escalation resolves. gate's inbox reduces parked runs by subject, so a
// re-park, a merge, or a keyboard judgment silently drops an older park out of
// the inbox — and its Approve button, still live in Slack, becomes a tap that
// fails with "not in gate inbox". Routing those state changes as pages instead
// would page the operator for every judgment gate ever records; applying them
// keeps the card honest without adding a single notification.
//
// The zero value is ClassPage, so an event built without thinking about this
// still notifies — absence must never read as not-page-worthy.
type Class int

// Event classes.
const (
	ClassPage Class = iota
	ClassUpdate
)

// Card-update outcomes: the closed vocabulary an UPDATE event carries in
// Fields["outcome"], naming how a park ended. The source writes them and notify
// renders them, so the spelling lives here where both see it rather than as
// literals on each side.
//
// MergeAuthorized is deliberately not "merged". gate's action artifact records
// the authorization and the exact head it pins; the merge itself is run by the
// caller, and gate keeps no receipt that one landed. Calling it merged would
// claim a fact nothing recorded.
const (
	OutcomeApproved        = "approved"
	OutcomeBlocked         = "blocked"
	OutcomeDecided         = "decided"
	OutcomeMergeAuthorized = "merge-authorized"
	OutcomeSuperseded      = "superseded"
)

// Event is one push-worthy fact. ID is stable and unique per fact (the
// producer's artifact ID where one exists) — dedupe keys on it, so a
// restart-and-resweep never re-pages.
type Event struct {
	Class    Class     // page (routed) or update (applied to a delivered card)
	Source   string    // config source name, e.g. "gate"
	ID       string    // dedupe key: artifact ID / receipt key+outcome
	Kind     string    // producer kind: "escalation", "verdict", "receipt", "cursor-alert"
	Time     time.Time // producer's timestamp
	Severity Severity
	Title    string // one line, e.g. "gate: ship#181 parked for judgment"
	Body     string // the detail a notification carries

	// Match fields beyond source/kind, filled per source: "decision",
	// "outcome", "code". Routes select on these.
	Fields map[string]string
}
