package source

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
	"github.com/itsHabib/workbench/contracts"
)

// escalationBody is gate's escalation payload. It is a body kind flare renders
// but not part of the shared verdict contract, so it stays local here and is
// read tolerantly — a missing body still notifies. The envelope and the verdict
// come from contracts; this shape does not.
type escalationBody struct {
	Outcome  string `json:"outcome"`
	Question string `json:"question"`
	Code     string `json:"code"`
}

// parseGateLog lifts events from gate artifact lines: every escalation, and
// every verdict whose decision is block or escalate. Everything else in the
// log (evidence, grants, actions, passing verdicts) is not push-worthy.
// Unparseable lines fail the whole read — a corrupt log must not read as
// quiet.
func parseGateLog(src config.Source, lines []string) ([]event.Event, string, error) {
	var events []event.Event
	last := ""
	for _, l := range lines {
		var env contracts.Envelope
		if err := json.Unmarshal([]byte(l), &env); err != nil {
			return nil, "", fmt.Errorf("source %s: bad artifact line: %w", src.Name, err)
		}
		last = env.Hash
		ev, ok, err := gateEvent(src, env)
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
func gateEvent(src config.Source, env contracts.Envelope) (event.Event, bool, error) {
	if env.Kind == contracts.KindEscalation {
		return escalationEvent(src, env), true, nil
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

func escalationEvent(src config.Source, env contracts.Envelope) event.Event {
	var b escalationBody
	json.Unmarshal(env.Body, &b) // tolerant: a missing body still notifies
	title := fmt.Sprintf("%s: parked for judgment (%s)", src.Name, env.Run)
	if b.Outcome != "" {
		title = fmt.Sprintf("%s: %s (%s)", src.Name, strings.ReplaceAll(b.Outcome, "_", " "), env.Run)
	}
	fields := map[string]string{"code": b.Code}
	if env.Run != "" {
		fields["run"] = env.Run
	}
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
