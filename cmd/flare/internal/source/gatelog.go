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
	if env.Kind == KindGrantNeeded {
		return grantNeededEvent(src, env, lg), true, nil
	}
	if ev, ok := updateEvent(src, env, lg); ok {
		return ev, true, nil
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
	reasonFields(fields, b.Question)
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

// reasonFields fingerprints the park's PRIMARY reason and separates the rest,
// so the delivery journal can tell an operator's third identical readiness
// question from a genuinely new one.
//
// gate packs a park's reasons into one "; "-joined line whose FIRST clause is
// the primary one — 318 of 355 parks in the live ledger lead with the identical
// readiness sentence, while the clauses after it vary. Fingerprinting the whole
// line therefore almost never matches (26 of 357 collapse) even though the
// operator is reading the same opening sentence over and over; fingerprinting
// the leading clause matches what they actually experience.
//
// The remaining clauses are kept separately rather than folded in, because they
// are the part that is NEW — a collapsed card shows them instead of hiding
// them. Nothing is dropped: the full reason is one `gate explain` away, and the
// card says so.
func reasonFields(fields map[string]string, question string) {
	if question == "" {
		return
	}
	lead, rest, _ := strings.Cut(question, "; ")
	fields["reason_id"] = fmt.Sprintf("%08x", noteHash(lead))
	fields["reason_lead"] = lead
	if rest != "" {
		fields["reason_rest"] = rest
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
	l := lg.get()
	// The head the verdict was gathered against, and the review budget spent so
	// far. Both are facts about THIS park that a card can lead with when the
	// reason itself is the same sentence the operator has read a hundred times.
	if head := l.headOf(b.Verdict); head != "" {
		fields["head"] = head
	}
	park := l.park(grantID, b.Verdict, b.Repo, b.Number, env.Run)
	if park.CyclesKnown && park.Grant.MaxCycles > 0 {
		fields["cycles_used"] = strconv.Itoa(park.Cycles)
		fields["cycles_max"] = strconv.Itoa(park.Grant.MaxCycles)
	}
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

// --- card updates ------------------------------------------------------------

// updateEvent lifts a gate artifact that CLOSES a park into a ClassUpdate
// event. These are not pages — routing them would notify the operator for every
// judgment gate ever records — they are the current state of a card already
// delivered, applied to it so a resolved park stops showing live Approve
// buttons.
//
// Three artifacts close a park, matching the three ways one actually ends:
//
//   - a JUDGMENT parented to the escalation — a keyboard `gate judge`, or the
//     judgment a Slack tap produced;
//   - a RESOLUTION parented to the escalation — the back-channel's stamp, which
//     carries the verified human identity;
//   - an ACTION with a merge outcome — the run reached authorization, which in
//     gate's inbox also resolves older parked attempts for the same PR.
//
// Each names a SUBJECT ("repo#n"), not just an escalation, because that is how
// gate's inbox reduces: only the latest terminal per subject stays parked, so a
// terminal artifact closes every older card for that PR too.
func updateEvent(src config.Source, env contracts.Envelope, lg *lazyLedger) (event.Event, bool) {
	fields, ok := updateFields(env, lg)
	if !ok {
		return event.Event{}, false
	}
	return event.Event{
		Class:    event.ClassUpdate,
		Source:   src.Name,
		ID:       env.ID,
		Kind:     "card-update",
		Time:     env.Time,
		Severity: event.SevInfo,
		Title:    fmt.Sprintf("%s: %s#%s %s", src.Name, fields["repo"], fields["number"], fields["outcome"]),
		Fields:   fields,
	}, true
}

func updateFields(env contracts.Envelope, lg *lazyLedger) (map[string]string, bool) {
	switch env.Kind {
	case contracts.KindJudgment:
		return judgmentUpdate(env, lg)
	case contracts.KindResolution:
		return resolutionUpdate(env, lg)
	case contracts.KindAction:
		return mergeUpdate(env, lg)
	}
	return nil, false
}

// judgmentBody is the slice of a judgment a card update reads.
//
// Who is consumed DEFENSIVELY: gate is adding a `who` to its decisions, and an
// artifact written before that lands simply omits it. An absent Who falls back
// to the producer's impl ("operator" for a keyboard judge), so an old record
// still renders a truthful card rather than an empty one.
type judgmentBody struct {
	Decision string `json:"decision"`
	Why      string `json:"why"`
	Who      string `json:"who"`
	Producer struct {
		Impl string `json:"impl"`
	} `json:"producer"`
}

func judgmentUpdate(env contracts.Envelope, lg *lazyLedger) (map[string]string, bool) {
	esc := parkParent(env)
	if esc == "" {
		return nil, false
	}
	var b judgmentBody
	if err := json.Unmarshal(env.Body, &b); err != nil {
		return nil, false
	}
	return updateMap(lg, esc, env.Run, b.Decision, judgeWho(lg, esc, b)), true
}

// judgeWho names the decider. It prefers the identity a RESOLUTION recorded for
// the same park — the verified human Slack authenticated — because gate writes
// the judgment first and its body names only the producer. Then gate's own
// `who` field on the judgment (additive; absent on an artifact written before
// it lands), then the producer's impl. It never invents one.
//
// The resolution lookup works because the LEDGER IS BUILT FROM THE WHOLE FILE,
// not the new-since-cursor slice (see newLazyLedger's call site in source.go):
// gate writes a park's terminal artifacts as one transaction, so the resolution
// is already indexed when the judgment that precedes it is turned into an
// event. Narrowing the ledger to the unread tail would silently degrade this to
// the producer's impl — a rename in an audit trail, not a crash — so the
// dependency is stated here rather than left to be rediscovered.
func judgeWho(lg *lazyLedger, esc string, b judgmentBody) string {
	if who := lg.get().resolvedWho(esc); who != "" {
		return who
	}
	if b.Who != "" {
		return b.Who
	}
	return b.Producer.Impl
}

func resolutionUpdate(env contracts.Envelope, lg *lazyLedger) (map[string]string, bool) {
	esc := parkParent(env)
	if esc == "" {
		return nil, false
	}
	var b escalation.Resolution
	if err := json.Unmarshal(env.Body, &b); err != nil {
		return nil, false
	}
	return updateMap(lg, esc, env.Run, b.Decision, b.Who), true
}

// mergeUpdate closes a card on the merge authorization: the run reached an
// action, so nothing about that PR is awaiting judgment any more. The subject
// comes from the action's parent verdict — an action body names no PR.
//
// An action with no recorded `--match-head-commit` yields NO update at all, and
// that is the intended degradation rather than an oversight: this path exists
// to add the pinned head, and a card with no head to add is already closed by
// the judgment or resolution for the same park, which always carries one. The
// worst case is a closed card missing its sha line — never a stale card.
func mergeUpdate(env contracts.Envelope, lg *lazyLedger) (map[string]string, bool) {
	if len(env.Parents) == 0 {
		return nil, false
	}
	l := lg.get()
	repo, number := l.subjectFromVerdict(env.Parents[0])
	sha := l.mergeSHA(env.Run)
	if repo == "" || number == 0 || sha == "" {
		return nil, false
	}
	return map[string]string{
		"repo":    repo,
		"number":  strconv.Itoa(number),
		"run":     env.Run,
		"outcome": event.OutcomeMergeAuthorized,
		"sha":     sha,
	}, true
}

// parkParent returns the escalation a terminal artifact closes. gate parents a
// judgment and a resolution to the escalation FIRST, so Parents[0] is the join;
// an artifact whose first parent is not an escalation closes no card.
func parkParent(env contracts.Envelope) string {
	if len(env.Parents) == 0 || !strings.HasPrefix(env.Parents[0], "esc_") {
		return ""
	}
	return env.Parents[0]
}

func updateMap(lg *lazyLedger, esc, run, decision, who string) map[string]string {
	l := lg.get()
	repo, number := l.subjectOf(esc)
	fields := map[string]string{
		"escalation": esc,
		"run":        run,
		"outcome":    decisionOutcome(decision),
	}
	if repo != "" && number > 0 {
		fields["repo"] = repo
		fields["number"] = strconv.Itoa(number)
	}
	if who != "" {
		fields["who"] = who
	}
	if sha := l.mergeSHA(run); sha != "" {
		fields["sha"] = sha
	}
	return fields
}

func decisionOutcome(decision string) string {
	if decision == escalation.DecisionBlock {
		return event.OutcomeBlocked
	}
	if decision == escalation.DecisionPass {
		return event.OutcomeApproved
	}
	return event.OutcomeDecided
}

// --- grant_needed: the operator's authority is the bottleneck ------------------

// KindGrantNeeded is gate's artifact kind for a run refused for want of
// authority. It is not in the shared contracts vocabulary yet, so it is named
// here as the string gate persists; flare reads it, never writes it.
const KindGrantNeeded = "grant_needed"

// Grant-refusal reasons gate records. grantAbsent and grantExpired come from a
// top-level capability refusal; grantCycleExceeded from the pre-flight cycle
// ceiling gate added in #242.
const (
	reasonAbsent  = "grant_absent"
	reasonExpired = "grant_expired"
	reasonCycles  = "grant_cycle_exceeded"
)

// grantNeededBody is gate's persisted refusal record. It has two shapes — a
// bare {repo, reason, at} for an absent or expired grant, and a richer one
// under a run for a spent cycle budget — and both decode into this one struct
// with the absent fields left zero.
type grantNeededBody struct {
	Repo       string `json:"repo"`
	Number     int    `json:"number"`
	Reason     string `json:"reason"`
	Grant      string `json:"grant"`
	CyclesUsed int    `json:"cycles_used"`
	CyclesMax  int    `json:"cycles_max"`
	Why        string `json:"why"`
}

// grantNeededEvent lifts a refusal into its own page class.
//
// This is the ONE alert that means "your authority is the bottleneck": an agent
// can re-run, re-review and re-judge, but it cannot mint — and the operator
// cannot mint from a phone either. So the page must arrive EARLY, while they
// are still near a keyboard, rather than being discovered later as stalled
// work. flare read these artifacts and dropped them; every one is a run that
// stopped dead.
//
// It carries the paste-ready mint, because the remedy is a single command the
// operator runs at a keyboard and the whole job of the card is to make that one
// paste instead of an investigation.
func grantNeededEvent(src config.Source, env contracts.Envelope, lg *lazyLedger) event.Event {
	var b grantNeededBody
	_ = json.Unmarshal(env.Body, &b) // tolerant: a body flare cannot read still pages, with less detail
	fields := map[string]string{
		"reason": b.Reason,
		"repo":   b.Repo,
		"state":  filepath.Dir(src.Path),
	}
	if env.Run != "" {
		fields["run"] = env.Run
	}
	if b.Number > 0 {
		fields["number"] = strconv.Itoa(b.Number)
	}
	if b.CyclesMax > 0 {
		fields["cycles_used"] = strconv.Itoa(b.CyclesUsed)
		fields["cycles_max"] = strconv.Itoa(b.CyclesMax)
	}
	fields["mint"] = grantNeededMint(b, lg, filepath.Dir(src.Path))
	return event.Event{
		Source:   src.Name,
		ID:       env.ID,
		Kind:     "grant-needed",
		Time:     env.Time,
		Severity: event.SevEscalate,
		Title:    fmt.Sprintf("%s: %s needs a grant (%s)", src.Name, b.Repo, b.Reason),
		Body:     grantNeededWhy(b),
		Fields:   fields,
	}
}

// grantNeededMint builds the remedy for each refusal reason. A spent cycle
// budget needs a WIDER -max-cycles at the same tier; an absent or expired grant
// needs a fresh one, and the last grant the repo held is the best evidence of
// the ceiling it actually works at.
func grantNeededMint(b grantNeededBody, lg *lazyLedger, stateDir string) string {
	if b.Repo == "" {
		return ""
	}
	tier, cycles := lastCeilings(b.Repo, b.Grant, lg)
	if b.Reason == reasonCycles {
		return preflight.Mint(b.Repo, stateDir, tier, b.CyclesUsed+1)
	}
	return preflight.Mint(b.Repo, stateDir, tier, cycles)
}

// lastCeilings recovers the ceilings to re-mint at: the refused grant's own
// when the record names one, else the widest ceiling the repo has held. Both
// are recorded facts — flare proposes what worked before, it never widens.
func lastCeilings(repo, grantID string, lg *lazyLedger) (string, int) {
	l := lg.get()
	if g, ok := l.grants[grantID]; ok {
		return g.MaxTier, g.MaxCycles
	}
	return l.lastCeilingsFor(repo)
}

func grantNeededWhy(b grantNeededBody) string {
	if b.Why != "" {
		return b.Why
	}
	switch b.Reason {
	case reasonAbsent:
		return "gate found no live grant for this repo, so it refused before gathering any evidence. Nothing proceeds here until one is minted."
	case reasonExpired:
		return "the grant this repo was running under has expired. gate refuses every run until a fresh one is minted."
	case reasonCycles:
		return "this PR has spent its grant's review-cycle budget. The ceiling is the stop signal: check whether the review loop is looping before widening it."
	}
	return "gate refused a run for want of authority."
}
