package source

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
	"github.com/itsHabib/workbench/cmd/flare/internal/preflight"
	"github.com/itsHabib/workbench/contracts"
	"github.com/itsHabib/workbench/contracts/escalation"
)

// The escalation body flare renders is now the shared contract
// (contracts/escalation.V1), decoded through escalation.DecodeBody —
// the same tolerant read this file used to do against a locally-redeclared
// struct. Tolerance is preserved deliberately: DecodeBody applies no version
// gate, and flare treats any decode error as "no body", so an older or drifted
// escalation still notifies rather than failing the read. flare stays a pure
// sink (Amendment 3): it renders this contract, it never writes into it.

// parseGateLog lifts events from gate artifact lines: every escalation, and
// every verdict whose decision is block or escalate. Everything else in the
// log (evidence, grants, actions, passing verdicts) is not push-worthy.
// Unparseable lines fail the whole read — a corrupt log must not read as
// quiet.
func parseGateLog(src config.Source, lines []string, lg *lazyLedger) ([]event.Event, string, error) {
	var events []event.Event
	last := ""
	for _, l := range lines {
		var env contracts.Envelope
		if err := json.Unmarshal([]byte(l), &env); err != nil {
			return nil, "", fmt.Errorf("source %s: bad artifact line: %w", src.Name, err)
		}
		last = env.Hash
		ev, ok, err := gateEvent(src, env, lg)
		if err != nil {
			return nil, "", fmt.Errorf("source %s: %w", src.Name, err)
		}
		if !ok {
			continue
		}
		events = append(events, ev)
	}
	return events, last, nil
}

// gateEvent dispatches one artifact by kind. Decoding the verdict body is the
// contract's job (Envelope.Verdict); deciding whether a verdict is page-worthy
// is flare's. That split is the whole point of the shared package: flare no
// longer hand-parses the verdict schema.
//
// A verdict whose body will not decode is a corrupt artifact, not a non-event:
// it fails the read loudly (like a corrupt envelope line), so a block/escalate
// can never vanish quietly and go unpaged. Only ok=false — a kind that is not a
// verdict at all — is a legitimate skip.
func gateEvent(src config.Source, env contracts.Envelope, lg *lazyLedger) (event.Event, bool, error) {
	if env.Kind == contracts.KindEscalation {
		return escalationEvent(src, env, lg), true, nil
	}
	v, ok, err := env.Verdict()
	if err != nil {
		return event.Event{}, false, fmt.Errorf("verdict %s: %w", env.ID, err)
	}
	if !ok {
		return event.Event{}, false, nil
	}
	ev, page := verdictEvent(src, env, v)
	return ev, page, nil
}

func escalationEvent(src config.Source, env contracts.Envelope, lg *lazyLedger) event.Event {
	b, _ := escalation.DecodeBody(env.Body) // tolerant: an undecodable body yields the zero value (Brief nil → briefed "no" → drops under the briefed routes), never a corrupt page
	title := fmt.Sprintf("%s: parked for judgment (%s)", src.Name, env.Run)
	if b.Outcome != "" {
		title = fmt.Sprintf("%s: %s (%s)", src.Name, strings.ReplaceAll(b.Outcome, "_", " "), env.Run)
	}
	fields := map[string]string{"code": b.Code}
	if env.Run != "" {
		fields["run"] = env.Run
	}
	// The state directory the watched ledger lives in (the log's parent — gate
	// writes log.jsonl directly under -state). notify splices it into the
	// resolve line as -state: the watched path is explicit flare config, so
	// the pasted command must pin the ledger rather than trust the paster's
	// ambient $GATE_STATE to point at the same one.
	fields["state"] = filepath.Dir(src.Path)
	// The PR subject, when the escalation names one, feeds notify's headline
	// and View PR button — the click-target verdicts already get.
	if b.Repo != "" && b.Number > 0 {
		fields["repo"] = b.Repo
		fields["number"] = strconv.Itoa(b.Number)
	}
	// The grant, when the park ran under one. notify keys the resolve buttons
	// on it: a grantless park (schema-valid since escalation.v1's second
	// consumer) resolves out-of-band, so Approve/Block would offer a tap
	// escalate's ingest is guaranteed to refuse. A "none:"-prefixed value is
	// the documented pre-amendment sentinel for that same situation ("I hold no
	// grant"), so a persisted sentinel body lifts as grantless, not as a grant.
	if b.Grant != "" && !strings.HasPrefix(b.Grant, "none:") {
		fields["grant"] = b.Grant
	}
	// A routable signal for brief-presence: routing pages a human only for
	// escalations gate briefed, and keeps procedural/no-brief parks off Slack.
	fields["briefed"] = "no"
	if b.Brief != nil {
		fields["briefed"] = "yes"
	}
	briefFields(fields, b.Brief)
	preflightFields(fields, src, env, b, fields["grant"], lg)
	return event.Event{
		Source:   src.Name,
		ID:       env.ID,
		Kind:     "escalation",
		Time:     env.Time,
		Severity: event.SevEscalate,
		Title:    title,
		Body:     b.Question,
		Fields:   fields,
	}
}

// briefFields flattens the optional brief into event fields, so notify can
// render its sections without knowing gate's body shape. Only non-empty
// fields land — notify treats absence as "no brief, quote the question".
func briefFields(fields map[string]string, b *escalation.Brief) {
	if b == nil {
		return
	}
	for k, v := range map[string]string{
		"brief_what":    b.WhatItIs,
		"brief_concern": b.Concern,
		"brief_risk":    b.Risk,
		"brief_rec":     b.Recommendation,
	} {
		if v == "" {
			continue
		}
		fields[k] = v
	}
}

// preflightFields records whether an Approve tap on this park could land, and
// — when it could not — the blocker, the authority that clears it, and the
// paste-ready mint command. The facts are gate's own (the grant's ceilings and
// the verdict's tier, joined by the ids the park itself carries); flare only
// reads them forward so notify can decline to paint a doomed button.
//
// It covers the gap notify's ceilingPark cannot: that check reads the park's
// OWN code, so it withholds buttons on a park that already announced a ceiling.
// A CONTENT park carries no code, and its ceilings are only checked downstream —
// which is how workbench#242 got an Approve button on a T3 verdict under a T1
// grant, recorded a judgment, and then died on grant_tier_exceeded. The tap was
// one-shot; the refusal was arithmetic anyone holding both artifacts could have
// done first.
//
// Absence is the fail-open signal: an unresolvable join writes no `approvable`
// field at all, and notify renders exactly the card it renders today. flare
// never withholds the operator's remote path on a guess.
//
// grantID is the caller's already-sanitized grant (empty for a grantless or
// "none:"-sentinel park), so the sentinel is skipped here for free.
func preflightFields(fields map[string]string, src config.Source, env contracts.Envelope, b escalation.V1, grantID string, lg *lazyLedger) {
	if grantID == "" || b.Verdict == "" {
		return
	}
	park := lg.get().park(grantID, b.Verdict, b.Repo, b.Number, env.Run)
	res := preflight.Check(park, time.Now())
	if !res.Known {
		return
	}
	fields["verdict_tier"] = park.VerdictTier
	fields["grant_tier"] = park.Grant.MaxTier
	if res.Approve {
		fields["approvable"] = "yes"
		return
	}
	fields["approvable"] = "no"
	fields["blocker"] = res.Blocker
	fields["needs"] = res.Needs
	// gate's state dir is the directory holding the log flare was CONFIGURED to
	// watch — read off that configured path, never a hardcoded sibling.
	fields["mint"] = preflight.MintCommand(b.Repo, filepath.Dir(src.Path), res)
}

// verdictEvent renders a page-worthy verdict into an event. Only block and
// escalate page; a passing verdict is not a notification. Identity and time
// come from the envelope, the decision from the verdict.
func verdictEvent(src config.Source, env contracts.Envelope, v contracts.Verdict) (event.Event, bool) {
	if v.Decision != contracts.DecisionBlock && v.Decision != contracts.DecisionEscalate {
		return event.Event{}, false
	}
	sev := event.SevEscalate
	if v.Decision == contracts.DecisionBlock {
		sev = event.SevBlock
	}
	subject := fmt.Sprintf("%s#%d", v.Subject.Repo, v.Subject.Number)
	return event.Event{
		Source:   src.Name,
		ID:       env.ID,
		Kind:     "verdict",
		Time:     env.Time,
		Severity: sev,
		Title:    fmt.Sprintf("%s: %s %s (%s, %s)", src.Name, subject, v.Decision, v.Source, v.Tier),
		Body:     v.Why,
		Fields:   verdictFields(env, v),
	}, true
}

// verdictFields carries the verdict's structured facts so notify can render
// clean fields and a PR link. Routes still select on "decision"; the rest are
// presentational and absent when the verdict does not name them.
func verdictFields(env contracts.Envelope, v contracts.Verdict) map[string]string {
	fields := map[string]string{"decision": v.Decision}
	if env.Run != "" {
		fields["run"] = env.Run
	}
	if v.Subject.Repo != "" {
		fields["repo"] = v.Subject.Repo
	}
	if v.Subject.Number > 0 {
		fields["number"] = strconv.Itoa(v.Subject.Number)
	}
	if v.Tier != "" {
		fields["tier"] = v.Tier
	}
	if v.Source != "" {
		fields["dimension"] = v.Source
	}
	return fields
}
