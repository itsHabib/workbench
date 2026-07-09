// Package route matches events against the declarative routes table and
// applies the throttle. Routing never drops an event silently: no match
// falls through to the catch-all channel, and silence requires an explicit
// route to the drop channel.
package route

import (
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
)

// Decision is where one event goes. Throttled events are still journaled by
// the caller — skipped is a recorded fact, not silence.
type Decision struct {
	Channel   string
	Throttled bool
}

// Router applies the routes table. Throttle state is per-route and
// in-memory: a one-shot sweep starts cold, which errs toward notifying.
type Router struct {
	cfg  config.Config
	now  func() time.Time
	last map[int]delivery // route index -> most recent pass-through
}

type delivery struct {
	at  time.Time
	sev event.Severity
}

// New builds a router over cfg's routes table; now is injected so throttle
// windows are testable.
func New(cfg config.Config, now func() time.Time) *Router {
	return &Router{cfg: cfg, now: now, last: map[int]delivery{}}
}

// Route picks the channel for ev. The throttle is severity-monotone: within
// a route's window a strictly worse event still passes — worst wins.
func (r *Router) Route(ev event.Event) Decision {
	for i, rt := range r.cfg.Routes {
		if !matches(rt.Match, ev) {
			continue
		}
		if r.throttled(i, rt, ev) {
			return Decision{Channel: rt.Channel, Throttled: true}
		}
		r.last[i] = delivery{at: r.now(), sev: ev.Severity}
		return Decision{Channel: rt.Channel}
	}
	return Decision{Channel: r.cfg.CatchAll}
}

func (r *Router) throttled(i int, rt config.Route, ev event.Event) bool {
	if rt.ThrottleSeconds <= 0 {
		return false
	}
	prev, ok := r.last[i]
	if !ok {
		return false
	}
	window := time.Duration(rt.ThrottleSeconds) * time.Second
	if r.now().Sub(prev.at) >= window {
		return false
	}
	return ev.Severity <= prev.sev
}

func matches(m config.Match, ev event.Event) bool {
	if !matchField(m.Source, ev.Source) {
		return false
	}
	if !matchField(m.Kind, ev.Kind) {
		return false
	}
	if !matchField(m.Decision, ev.Fields["decision"]) {
		return false
	}
	if !matchField(m.Outcome, ev.Fields["outcome"]) {
		return false
	}
	return matchField(m.Code, ev.Fields["code"])
}

// matchField: empty pattern matches anything; otherwise exact match against
// any "|"-separated alternative.
func matchField(pattern, value string) bool {
	if pattern == "" {
		return true
	}
	for _, alt := range strings.Split(pattern, "|") {
		if alt == value {
			return true
		}
	}
	return false
}
