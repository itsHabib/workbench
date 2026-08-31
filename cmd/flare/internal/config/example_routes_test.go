package config

import (
	"strings"
	"testing"
)

// exampleRoutes is the darwin runbook's starting point: the operator copies it
// to ~/.flare/routes.json and fills in the placeholders. It is committed
// documentation, so it drifts from the binary the moment nobody loads it —
// the same drift class the golden routes test pins for the shipped shape.
const exampleRoutes = "../../scripts/routes.example.json"

func TestConfigLoadAcceptsScriptsExampleRoutes(t *testing.T) {
	cfg, err := Load(exampleRoutes)
	if err != nil {
		t.Fatalf("the committed routes example must load against this binary: %v", err)
	}
	if cfg.CatchAll == "" {
		t.Fatal("example routes must carry a catch_all: an unrouted event cannot read as calm")
	}
	kinds := map[string]bool{}
	for _, s := range cfg.Sources {
		kinds[s.Kind] = true
	}
	for _, want := range []string{SourceGateLog, SourceShipReceipts} {
		if !kinds[want] {
			t.Fatalf("example routes missing a %q source; source kinds = %v", want, kinds)
		}
	}
	// The whole point of the darwin slice is a live Approve/Block tap, so the
	// example must show resolve_actions wired on a slack channel that a gate
	// escalation actually routes to.
	resolvers := map[string]bool{}
	for name, ch := range cfg.Channels {
		if ch.Type == ChannelSlack && ch.ResolveActions {
			resolvers[name] = true
		}
	}
	if len(resolvers) == 0 {
		t.Fatal("example routes must set resolve_actions on a slack channel")
	}
	if !routesEscalationTo(cfg, resolvers) {
		t.Fatalf("no gate escalation route targets a resolve_actions channel; resolvers = %v", resolvers)
	}
	for name, ch := range cfg.Channels {
		if ch.Type != ChannelSlack {
			continue
		}
		if !strings.Contains(ch.Token, "REPLACE") {
			t.Fatalf("channel %q token must be a placeholder, never a real secret: %q", name, ch.Token)
		}
	}
}

// routesEscalationTo reports whether a gate escalation route lands on one of
// the named channels.
func routesEscalationTo(cfg Config, channels map[string]bool) bool {
	for _, r := range cfg.Routes {
		if r.Match.Source == "gate" && r.Match.Kind == "escalation" && channels[r.Channel] {
			return true
		}
	}
	return false
}

// A gate run emits the reducer's fold AND every component verdict it folded,
// and the fold restates the worst component's `why`. An example that routes on
// decision alone therefore ships a channel that says the same thing once per
// rung — 308 of 396 cards on the operator's own feed by 2026-08-29, none of
// them closeable (only escalation cards are tracked for correction). The
// example must show the BLOCK-fold paging and its parts explicitly silenced.
// The escalate-fold is deliberately not in that set: a parked run is announced
// by its escalation artifact, which is also the only card flare can go back and
// close.
func TestExampleRoutesDoNotPageEveryLadderRung(t *testing.T) {
	cfg, err := Load(exampleRoutes)
	if err != nil {
		t.Fatalf("load example routes: %v", err)
	}
	var foldPages, componentsDrop bool
	for _, r := range cfg.Routes {
		if r.Match.Source != "gate" || r.Match.Kind != "verdict" {
			continue
		}
		if r.Match.Dimension == "reducer" && r.Match.Decision == "block" && r.Channel != ChannelDrop {
			foldPages = true
		}
		if r.Match.Dimension == "" && r.Channel == ChannelDrop {
			componentsDrop = true
		}
	}
	if !foldPages {
		t.Fatal("example routes must page the run-level fold (dimension: reducer): a blocked run has to announce itself once")
	}
	if !componentsDrop {
		t.Fatal("example routes must explicitly drop the remaining verdict rungs: they restate the fold, and a verdict card can never be closed")
	}
}
