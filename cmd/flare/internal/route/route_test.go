package route

import (
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
)

func cfg() config.Config {
	return config.Config{
		Channels: map[string]config.Channel{"toast": {Type: config.ChannelToast}},
		Routes: []config.Route{
			{Match: config.Match{Source: "gate", Kind: "escalation"}, Channel: "toast", ThrottleSeconds: 600},
			{Match: config.Match{Source: "ship", Outcome: "failed|cancelled"}, Channel: config.ChannelDrop},
		},
		CatchAll: "toast",
	}
}

func TestUnmatchedFallsThroughToCatchAll(t *testing.T) {
	r := New(cfg(), time.Now)
	d := r.Route(event.Event{Source: "gate", Kind: "cursor-alert"})
	if d.Channel != "toast" || d.Throttled {
		t.Fatalf("unrouted event must hit the catch-all undropped, got %+v", d)
	}
}

func TestAlternationMatch(t *testing.T) {
	r := New(cfg(), time.Now)
	ev := event.Event{Source: "ship", Kind: "receipt", Fields: map[string]string{"outcome": "cancelled"}}
	if d := r.Route(ev); d.Channel != config.ChannelDrop {
		t.Fatalf("outcome alternation should match, got %+v", d)
	}
}

func TestBriefedEscalationRoutesByBrief(t *testing.T) {
	c := config.Config{
		Channels: map[string]config.Channel{"phone": {Type: config.ChannelSlack}, "drop": {Type: config.ChannelDrop}},
		Routes: []config.Route{
			{Match: config.Match{Source: "gate", Kind: "escalation", Briefed: "yes"}, Channel: "phone"},
			{Match: config.Match{Source: "gate", Kind: "escalation", Briefed: "no"}, Channel: "drop"},
		},
		CatchAll: "phone", // a real notifying catch-all (config.validate refuses a drop catch-all)
	}
	r := New(c, time.Now)
	briefed := event.Event{Source: "gate", Kind: "escalation", Fields: map[string]string{"briefed": "yes"}}
	if d := r.Route(briefed); d.Channel != "phone" {
		t.Fatalf("a briefed escalation must page phone, got %+v", d)
	}
	procedural := event.Event{Source: "gate", Kind: "escalation", Fields: map[string]string{"briefed": "no"}}
	if d := r.Route(procedural); d.Channel != "drop" {
		t.Fatalf("a no-brief (procedural) escalation must drop, not page, got %+v", d)
	}
}

func TestThrottleIsSeverityMonotone(t *testing.T) {
	now := time.Now()
	r := New(cfg(), func() time.Time { return now })
	esc := event.Event{Source: "gate", Kind: "escalation", Severity: event.SevEscalate}
	block := event.Event{Source: "gate", Kind: "escalation", Severity: event.SevBlock}

	if d := r.Route(esc); d.Throttled {
		t.Fatal("first event must pass")
	}
	if d := r.Route(esc); !d.Throttled {
		t.Fatal("equal severity inside the window must throttle")
	}
	if d := r.Route(block); d.Throttled {
		t.Fatal("a strictly worse event must pass through an open throttle window")
	}
	if d := r.Route(esc); !d.Throttled {
		t.Fatal("worst-wins: the window now holds block, escalate must throttle")
	}
	now = now.Add(11 * time.Minute)
	if d := r.Route(esc); d.Throttled {
		t.Fatal("an expired window must pass")
	}
}

// A gate run emits the reducer's fold AND every component verdict it folded,
// and the fold restates the worst component's `why`. Routing on decision alone
// therefore pages the same sentence once per rung — the ivy#50 shape, two
// identical "don't merge" cards a minute apart. Pinning: the fold pages, its
// parts go quiet, and the run is still announced exactly once.
func TestDimensionSeparatesTheFoldFromItsComponents(t *testing.T) {
	c := config.Config{
		Channels: map[string]config.Channel{"phone": {Type: config.ChannelSlack}, "quiet": {Type: config.ChannelDrop}},
		Routes: []config.Route{
			{Match: config.Match{Source: "gate", Kind: "verdict", Decision: "block", Dimension: "reducer"}, Channel: "phone"},
			{Match: config.Match{Source: "gate", Kind: "verdict"}, Channel: "quiet"},
		},
		CatchAll: "phone",
	}
	r := New(c, time.Now)
	verdict := func(dimension string) event.Event {
		return event.Event{Source: "gate", Kind: "verdict", Fields: map[string]string{"decision": "block", "dimension": dimension}}
	}
	if d := r.Route(verdict("reducer")); d.Channel != "phone" {
		t.Fatalf("the run-level fold must page, got %+v", d)
	}
	for _, component := range []string{"readiness", "review-panel-completeness", "review-consolidation"} {
		if d := r.Route(verdict(component)); d.Channel != "quiet" {
			t.Fatalf("component rung %q restates the fold and must stay quiet, got %+v", component, d)
		}
	}
}

// An unset Dimension must keep matching every verdict: the selector is opt-in,
// so a routes file written before it existed routes exactly as it did.
func TestUnsetDimensionMatchesAnyRung(t *testing.T) {
	c := config.Config{
		Channels: map[string]config.Channel{"phone": {Type: config.ChannelSlack}},
		Routes:   []config.Route{{Match: config.Match{Source: "gate", Decision: "block"}, Channel: "phone"}},
		CatchAll: "phone",
	}
	r := New(c, time.Now)
	for _, dimension := range []string{"reducer", "readiness", ""} {
		ev := event.Event{Source: "gate", Kind: "verdict", Fields: map[string]string{"decision": "block", "dimension": dimension}}
		if d := r.Route(ev); d.Channel != "phone" {
			t.Fatalf("an unset dimension must match rung %q, got %+v", dimension, d)
		}
	}
}
