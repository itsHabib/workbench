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
