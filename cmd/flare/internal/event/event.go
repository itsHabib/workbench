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

// Event is one push-worthy fact. ID is stable and unique per fact (the
// producer's artifact ID where one exists) — dedupe keys on it, so a
// restart-and-resweep never re-pages.
type Event struct {
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
