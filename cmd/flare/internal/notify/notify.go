// Package notify delivers one event to one channel. Delivery is best-effort
// and at-least-once-attempted; a failure is returned, journaled by the
// caller, and retried on the next poll because the cursor does not advance
// past an undelivered event.
package notify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
	"github.com/itsHabib/workbench/contracts/escalation"
)

const (
	slackPostMessageURL = "https://slack.com/api/chat.postMessage"
	slackTextLimit      = 4000 // notification/preview text cap
	slackSectionLimit   = 2900 // a section text block is rejected over ~3000 runes
)

// Send delivers one event to one channel; the drop channel succeeds without
// delivering anywhere.
func Send(ch config.Channel, ev event.Event) error {
	switch ch.Type {
	case config.ChannelToast:
		return toast(ev)
	case config.ChannelWebhook:
		return webhook(ch.URL, ev)
	case config.ChannelSlack:
		return slack(ch.Token, ch.ChannelID, ch.ResolveActions, ev)
	case config.ChannelDrop:
		return nil
	}
	return fmt.Errorf("notify: unknown channel type %q", ch.Type)
}

// A Slack message is one severity-colored attachment whose blocks lead on the
// action the operator must take — the whole point of a page is that what-to-do
// is unmistakable. The blocks live inside the attachment (not at the top level)
// so the message renders exactly once, as a single colored card; the
// attachment's Fallback is the notification/lock-screen line and is never shown
// in the channel body.
type slackRequest struct {
	Channel     string            `json:"channel"`
	Attachments []slackAttachment `json:"attachments,omitempty"`
}

type slackAttachment struct {
	Color    string       `json:"color"`
	Fallback string       `json:"fallback,omitempty"`
	Blocks   []slackBlock `json:"blocks"`
}

// slackBlock is one Block Kit block. Text carries header/section content;
// Elements carries a context block's text objects or an actions block's
// buttons (heterogeneous, hence any).
type slackBlock struct {
	Type     string     `json:"type"`
	Text     *slackText `json:"text,omitempty"`
	Elements []any      `json:"elements,omitempty"`
}

type slackText struct {
	Type  string `json:"type"`
	Text  string `json:"text"`
	Emoji bool   `json:"emoji,omitempty"`
}

// slackButton is one Block Kit button element in two flavors. A LINK button
// (View PR) carries a URL and Slack just opens it — no callback, no app needed.
// An INTERACTIVE button (Approve/Block) carries an ActionID and a Value instead:
// tapping it POSTs a signed interaction callback to the Slack app's configured
// Request URL, which fronts `escalate serve`. The two are mutually exclusive —
// URL is empty on an interactive button, ActionID/Value empty on a link — so a
// single struct with omitempty renders both without a union type.
type slackButton struct {
	Type     string    `json:"type"`
	Text     slackText `json:"text"`
	URL      string    `json:"url,omitempty"`
	ActionID string    `json:"action_id,omitempty"`
	Value    string    `json:"value,omitempty"`
	Style    string    `json:"style,omitempty"`
}

type slackResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func slack(token, channel string, resolveActions bool, ev event.Event) error {
	client := &http.Client{Timeout: 15 * time.Second}
	return postSlack(client, slackPostMessageURL, token, channel, resolveActions, ev)
}

func postSlack(client *http.Client, endpoint, token, channel string, resolveActions bool, ev event.Event) error {
	body, err := json.Marshal(renderSlackMessage(channel, resolveActions, ev))
	if err != nil {
		return fmt.Errorf("notify: slack channel %q: encode message: %w", channel, err)
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify: slack channel %q: build request: %w", channel, requestCause(err))
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("notify: slack channel %q: request: %w", channel, requestCause(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("notify: slack channel %q: status %s", channel, resp.Status)
	}
	var result slackResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("notify: slack channel %q: decode response: %w", channel, err)
	}
	if !result.OK {
		return fmt.Errorf("notify: slack channel %q: API error %q", channel, result.Error)
	}
	return nil
}

func requestCause(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// renderSlackMessage turns an event into a Block Kit message: one colored
// attachment whose blocks lead on the required action, plus a notification
// fallback so the lock-screen line still leads on the action.
func renderSlackMessage(channel string, resolveActions bool, ev event.Event) slackRequest {
	return slackRequest{
		Channel: channel,
		Attachments: []slackAttachment{{
			Color:    severityColor(ev.Severity),
			Fallback: slackFallback(ev),
			Blocks:   slackBlocks(ev, resolveActions),
		}},
	}
}

func slackBlocks(ev event.Event, resolveActions bool) []slackBlock {
	blocks := []slackBlock{
		{Type: "header", Text: &slackText{Type: "plain_text", Text: headline(ev), Emoji: true}},
	}
	// A synthesized brief is the card body when the producer sent one; else the raw reason renders as before.
	body := briefBlock(ev)
	if body == "" {
		body = whyBlock(ev.Body)
	}
	if body != "" {
		blocks = append(blocks, slackBlock{Type: "section", Text: &slackText{Type: "mrkdwn", Text: body}})
	}
	if blocker := blockerBlock(ev); blocker != "" {
		blocks = append(blocks, slackBlock{Type: "section", Text: &slackText{Type: "mrkdwn", Text: blocker}})
	}
	if actions := actionElements(ev, resolveActions); len(actions) > 0 {
		blocks = append(blocks, slackBlock{Type: "actions", Elements: actions})
	}
	if line := resolveLine(ev); line != "" {
		hint := slackText{Type: "mrkdwn", Text: line}
		blocks = append(blocks, slackBlock{Type: "context", Elements: []any{hint}})
	}
	footer := slackText{Type: "mrkdwn", Text: slackFooter(ev)}
	return append(blocks, slackBlock{Type: "context", Elements: []any{footer}})
}

// actionElements builds the card's one actions block: the View PR link when the
// event names a PR, then the Approve/Block resolve buttons when the channel has
// opted in AND the event is a resolvable park. The link and the resolve buttons
// share one actions row (Slack renders them side by side), so a briefed park on
// a resolve-actions channel gets "View PR · Approve · Block" — read, then act,
// without leaving Slack.
func actionElements(ev event.Event, resolveActions bool) []any {
	var elements []any
	if btn, ok := prButton(ev); ok {
		elements = append(elements, btn)
	}
	if !resolveActions || !resolvablePark(ev) {
		return elements
	}
	if approvable(ev) {
		elements = append(elements, approveButton(ev))
	}
	// Block survives a failed pre-flight on purpose. The ceilings that make an
	// approval un-landable are authorization ceilings on the MERGE path; there is
	// no ceiling that stops a human deciding "don't merge this", and the operator
	// away from a keyboard should still be able to say so.
	return append(elements, blockButton(ev))
}

// approvable reports whether the Approve button may be painted. The source
// records `approvable=no` only when it PROVED from gate's own artifacts that
// the tap could not land (see internal/preflight); an absent field means the
// facts did not support a verdict, and absence must render the card flare
// rendered before this check existed. Hence the default-yes read: flare
// withholds on a proof, never on a gap.
func approvable(ev event.Event) bool {
	return ev.Fields["approvable"] != "no"
}

// resolvablePark reports whether this event is one `escalate` can actually
// resolve: a gate PARK (Kind "escalation") that still carries its artifact id
// AND the grant it ran under. It deliberately excludes the other things that
// reach SevEscalate — a verdict with an escalate decision, a cursor-alert —
// because those are not parked escalations under a grant, so rendering
// Approve/Block on them would offer a tap that `gate resolve` would refuse.
// The id must be present because it is the button value the callback joins
// back to the parked run. The grant must be present because a grantless park
// (schema-valid since escalation.v1's second consumer) resolves out-of-band:
// escalate's ingest refuses an empty grant, so the buttons would be a tap
// guaranteed to fail. A ceiling park is excluded for the same reason from the
// other direction: `gate resolve` re-applies the grant's ceiling, so approving
// one re-parks it on the identical code. The remedies differ by code and
// neither is a tap: a tier park needs wider authority only the operator can
// mint; a cycle park is the stop signal that the process looped — the fix is
// fewer review rounds, never a wider grant.
func resolvablePark(ev event.Event) bool {
	if ev.Kind != "escalation" || ev.ID == "" || ev.Fields["grant"] == "" {
		return false
	}
	return !ceilingPark(ev.Fields["code"])
}

// ceilingPark reports whether the park stands on an authorization ceiling — the
// codes a decision cannot clear, mirroring gate's inbox projection.
func ceilingPark(code string) bool {
	return code == escalation.CodeTierExceeded || code == escalation.CodeCycleExceeded
}

// resolveLine is the paste-ready `escalate resolve` command for a resolvable
// park — the path that works from a phone with nothing but Slack and a terminal,
// independent of whether the channel opted into the Approve/Block buttons or the
// callback tunnel is up. The escalation id and the grant the run parked under are
// substituted verbatim; decision, who, and why stay placeholders because they are
// the human's to fill. Gated on resolvablePark for the same reason the buttons
// are: escalate's ingest refuses an empty grant, so a grantless park would get a
// command guaranteed to fail. When the event carries the watched ledger's state
// directory, the line pins it with -state: the watched path is explicit flare
// config, while the paster's terminal holds whatever $GATE_STATE it holds — an
// ambient-matching -state is harmless, a missing one against a non-ambient
// ledger resolves the wrong log. Rendering only — flare never runs it
// (Amendment 3).
func resolveLine(ev event.Event) string {
	if !resolvablePark(ev) {
		return ""
	}
	stateArg := ""
	if dir := ev.Fields["state"]; dir != "" {
		stateArg = " -state " + shellQuote(dir)
	}
	return fmt.Sprintf("`escalate resolve%s -escalation %s -grant %s -decision <pass|block> -who <you> -why \"...\"`",
		stateArg, ev.ID, ev.Fields["grant"])
}

// shellQuote makes a path safe to paste into a POSIX shell. A path of
// unambiguous characters passes through untouched — the common machine-managed
// state dir stays readable on the card — anything else is single-quoted, with
// embedded single quotes spliced as '\”. Without this, a state dir containing
// a space splits under word-splitting and escalate's flag parser receives a
// truncated -state, so the pasted command fails instead of resolving the park.
func shellQuote(s string) string {
	if !strings.ContainsAny(s, " \t\n'\"\\$`&|;<>()*?[]#~!{}") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// approveButton / blockButton render the two interactive resolve buttons. Each
// carries the SHARED action_id vocabulary (escalation.ActionApprove/ActionBlock)
// that escalate's serve parses, and the escalation artifact id as its value so a
// verified callback resolves the right park with nothing pasted. flare only
// paints them; the tap is handled by escalate, never flare (Amendment 3).
func approveButton(ev event.Event) slackButton {
	return slackButton{
		Type:     "button",
		Text:     slackText{Type: "plain_text", Text: "Approve", Emoji: true},
		ActionID: escalation.ActionApprove,
		Value:    ev.ID,
		Style:    "primary",
	}
}

func blockButton(ev event.Event) slackButton {
	return slackButton{
		Type:     "button",
		Text:     slackText{Type: "plain_text", Text: "Block", Emoji: true},
		ActionID: escalation.ActionBlock,
		Value:    ev.ID,
		Style:    "danger",
	}
}

// headline is the one line that must make the required action obvious: a plain
// imperative, with the subject woven in when the event names one.
func headline(ev event.Event) string {
	switch ev.Severity {
	case event.SevBlock:
		return blockHeadline(ev)
	case event.SevEscalate:
		return escalateHeadline(ev)
	case event.SevFailed:
		if ev.Fields["outcome"] == "parked" {
			return runHeadline(ev, "parked", "⏸️")
		}
		return runHeadline(ev, "failed", "❌")
	case event.SevCancelled:
		return runHeadline(ev, "cancelled", "⚪")
	}
	return "ℹ️ Notice"
}

func blockHeadline(ev event.Event) string {
	if s := subject(ev); s != "" {
		return "🛑 Don't merge " + s + " — review it yourself"
	}
	return "🛑 Blocked — this needs manual review"
}

func escalateHeadline(ev event.Event) string {
	s := subject(ev)
	// "Your call" is a lie when the recorded ceilings make an approval
	// un-landable: the call is not the operator's until they mint. Lead with the
	// authority that is actually missing so the lock-screen line is honest.
	if resolvablePark(ev) && !approvable(ev) {
		if s != "" {
			return "🔑 Grant needed for " + s + " — cannot approve as-is"
		}
		return "🔑 Grant needed — this park cannot be approved as-is"
	}
	if s != "" {
		return "⚠️ Your call on " + s
	}
	return "⚠️ Your call — a run paused for your decision"
}

// runHeadline covers failed and cancelled: prefer the task name, then the
// subject, then just the verb.
func runHeadline(ev event.Event, verb, icon string) string {
	if t := ev.Fields["task"]; t != "" {
		return icon + " Task " + verb + " — " + t
	}
	if s := subject(ev); s != "" {
		return icon + " Run " + verb + " — " + s
	}
	return icon + " Run " + verb
}

func severityColor(s event.Severity) string {
	switch s {
	case event.SevBlock:
		return "#C0143C"
	case event.SevEscalate:
		return "#E8912D"
	case event.SevFailed:
		return "#D64541"
	case event.SevCancelled:
		return "#9AA0A6"
	}
	return "#2F80ED"
}

// whyBlock renders the reason as a blockquote: a bulleted list when the producer
// packed several reasons into one "; "-joined line, otherwise a single quote.
// The words stay the producer's; flare only structures them.
func whyBlock(body string) string {
	why := compact(body)
	if why == "" {
		return ""
	}
	parts := strings.Split(why, "; ")
	if len(parts) == 1 {
		return truncateRunes("> "+why, slackSectionLimit)
	}
	lines := make([]string, len(parts))
	for i, p := range parts {
		lines[i] = "> • " + p
	}
	return truncateRunes(strings.Join(lines, "\n"), slackSectionLimit)
}

// briefBlock renders the producer's synthesized plain-language brief as
// labeled lines. Empty when the event carries no brief fields; the words are
// the producer's, flare only labels them.
func briefBlock(ev event.Event) string {
	var lines []string
	add := func(label, key string) {
		if v := compact(ev.Fields[key]); v != "" {
			lines = append(lines, "*"+label+":* "+v)
		}
	}
	add("What it is", "brief_what")
	add("The concern", "brief_concern")
	add("Risk", "brief_risk")
	add("Recommendation", "brief_rec")
	if len(lines) == 0 {
		return ""
	}
	return truncateRunes(strings.Join(lines, "\n"), slackSectionLimit)
}

// blockerBlock states, plainly, why this park cannot be approved from the phone
// and what would clear it — the section that replaces a button whose tap was
// guaranteed to fail. The mint command is fenced so it is one copy-paste at a
// keyboard: minting is the operator's authority alone, and the card's whole job
// is to make exercising it cost one paste instead of an investigation.
func blockerBlock(ev event.Event) string {
	if approvable(ev) {
		return ""
	}
	lines := []string{"*Cannot approve:* " + compact(ev.Fields["blocker"])}
	if needs := compact(ev.Fields["needs"]); needs != "" {
		lines = append(lines, "*Needs:* "+needs)
	}
	if mint := compact(ev.Fields["mint"]); mint != "" {
		lines = append(lines, "```"+mint+"```")
	}
	return truncateRunes(strings.Join(lines, "\n"), slackSectionLimit)
}

// subject is the short "repo#n" the header carries when the event names one.
func subject(ev event.Event) string {
	repo, num := ev.Fields["repo"], ev.Fields["number"]
	if repo == "" || num == "" {
		return ""
	}
	return shortRepo(repo) + "#" + num
}

func shortRepo(repo string) string {
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

// prButton links straight to the PR when repo+number are present — the one tap
// from a phone to the place the operator acts.
func prButton(ev event.Event) (slackButton, bool) {
	repo, num := ev.Fields["repo"], ev.Fields["number"]
	if repo == "" || num == "" {
		return slackButton{}, false
	}
	return slackButton{
		Type:  "button",
		Text:  slackText{Type: "plain_text", Text: "View PR #" + num, Emoji: true},
		URL:   "https://github.com/" + repo + "/pull/" + num,
		Style: "primary",
	}, true
}

// slackFooter is the small print: the source and the correlation ids the
// operator only needs when digging in, kept out of the way of the action above.
func slackFooter(ev event.Event) string {
	parts := []string{ev.Source}
	if tier := ev.Fields["tier"]; tier != "" {
		parts = append(parts, "tier "+tier)
	}
	if run := ev.Fields["run"]; run != "" {
		parts = append(parts, run)
	}
	parts = append(parts, ev.Time.Format("Jan 2, 3:04 PM MST"))
	return strings.Join(parts, " · ")
}

// slackFallback is the notification/preview text (the lock-screen line): the
// action first, then the reason — the brief's concern when the producer sent
// one, else the raw body — capped to Slack's text limit.
func slackFallback(ev event.Event) string {
	t := headline(ev)
	why := compact(ev.Fields["blocker"])
	if why == "" {
		why = compact(ev.Fields["brief_concern"])
	}
	if why == "" {
		why = compact(ev.Body)
	}
	if why != "" {
		t += " — " + why
	}
	return truncateRunes(t, slackTextLimit)
}

func compact(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	return string([]rune(s)[:limit-1]) + "…"
}

func webhook(url string, ev event.Event) error {
	body, err := json.Marshal(map[string]string{
		"source":   ev.Source,
		"id":       ev.ID,
		"kind":     ev.Kind,
		"severity": ev.Severity.String(),
		"title":    ev.Title,
		"body":     ev.Body,
		"time":     ev.Time.Format(time.RFC3339),
	})
	if err != nil {
		return fmt.Errorf("notify: encode event %s: %w", ev.ID, err)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("notify: webhook: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("notify: webhook: status %s", resp.Status)
	}
	return nil
}
