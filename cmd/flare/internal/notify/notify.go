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
	"github.com/itsHabib/workbench/contracts/grantrequest"
)

const (
	slackPostMessageURL = "https://slack.com/api/chat.postMessage"
	slackUpdateURL      = "https://slack.com/api/chat.update"

	// Method names for error text. They are literals, never derived from the
	// endpoint URL: an error must never carry the URL (a pinned test asserts it,
	// because the endpoint is configurable and an error is a place secrets leak).
	methodPost        = "chat.postMessage"
	methodUpdate      = "chat.update"
	slackTextLimit    = 4000 // notification/preview text cap
	slackSectionLimit = 2900 // a section text block is rejected over ~3000 runes
)

// Ref identifies one delivered Slack message so a later fact can update it in
// place. It is the whole reason Send has a return value: a card with no ref can
// never be corrected, and a card that is never corrected keeps showing live
// Approve buttons for a park that resolved hours ago.
//
// Its zero value means "no updatable message" — every channel type but slack
// returns one, and callers must treat it as "nothing to finalize" rather than
// an error.
type Ref struct {
	ChannelID string `json:"channel_id"`
	TS        string `json:"ts"`
}

// Updatable reports whether this ref names a message that can be corrected.
func (r Ref) Updatable() bool { return r.ChannelID != "" && r.TS != "" }

// Send delivers one event to one channel; the drop channel succeeds without
// delivering anywhere. The returned Ref names the posted Slack message (zero
// for every other channel type).
func Send(ch config.Channel, ev event.Event) (Ref, error) {
	switch ch.Type {
	case config.ChannelToast:
		return Ref{}, toast(ev)
	case config.ChannelWebhook:
		return Ref{}, webhook(ch.URL, ev)
	case config.ChannelSlack:
		return slack(ch.Token, ch.ChannelID, ch.ResolveActions, ev)
	case config.ChannelDrop:
		return Ref{}, nil
	}
	return Ref{}, fmt.Errorf("notify: unknown channel type %q", ch.Type)
}

// Finalize corrects a delivered card to the terminal state of its park: the
// buttons come off, the outcome and who decided go on, and the authorized head
// sha goes on when the run reached one. It then posts that outcome as a THREAD
// REPLY, because an updated card alone leaves a successful tap and a silently
// failed one looking identical — the reply is the receipt.
//
// The two calls are not atomic and deliberately are not made so. flare's
// delivery posture is at-least-once: a retry that re-posts a thread reply is
// the safe way to err, where a card left showing a live Approve button is not.
func Finalize(ch config.Channel, ref Ref, ev event.Event) error {
	if ch.Type != config.ChannelSlack || !ref.Updatable() {
		return nil
	}
	client := &http.Client{Timeout: 15 * time.Second}
	return finalizeSlack(client, slackUpdateURL, slackPostMessageURL, ch.Token, ref, ev)
}

// finalizeSlack is Finalize with its endpoints injected, mirroring postSlack —
// the seam that lets a test drive the real payloads against an httptest server.
func finalizeSlack(client *http.Client, updateURL, postURL, token string, ref Ref, ev event.Event) error {
	c := call{client: client, token: token, channel: ref.ChannelID}
	c.endpoint, c.method, c.payload = updateURL, methodUpdate, renderFinalCard(ref, ev)
	if _, err := callSlack(c); err != nil {
		return err
	}
	c.endpoint, c.method, c.payload = postURL, methodPost, slackReply{
		Channel:  ref.ChannelID,
		ThreadTS: ref.TS,
		Text:     outcomeLine(ev),
	}
	_, err := callSlack(c)
	return err
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

// slackUpdate is chat.update's shape: the same card, addressed to an existing
// message by channel + ts instead of posted fresh.
type slackUpdate struct {
	Channel     string            `json:"channel"`
	TS          string            `json:"ts"`
	Text        string            `json:"text"`
	Attachments []slackAttachment `json:"attachments,omitempty"`
}

// slackReply is a plain threaded message — the terminal receipt under the card.
type slackReply struct {
	Channel  string `json:"channel"`
	ThreadTS string `json:"thread_ts"`
	Text     string `json:"text"`
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
	// TS and Channel identify the posted message. Slack echoes the resolved
	// channel id even when the request named one, so the ref is always the id
	// chat.update will accept.
	TS      string `json:"ts"`
	Channel string `json:"channel"`
}

func slack(token, channel string, resolveActions bool, ev event.Event) (Ref, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	return postSlack(client, slackPostMessageURL, token, channel, resolveActions, ev)
}

func postSlack(client *http.Client, endpoint, token, channel string, resolveActions bool, ev event.Event) (Ref, error) {
	res, err := callSlack(call{
		client: client, endpoint: endpoint, token: token,
		method: methodPost, channel: channel,
		payload: renderSlackMessage(channel, resolveActions, ev),
	})
	if err != nil {
		return Ref{}, err
	}
	return Ref{ChannelID: refChannel(res.Channel, channel), TS: res.TS}, nil
}

// refChannel prefers the channel id Slack echoed back, falling back to the one
// configured. chat.update needs an id; a config naming a #name would otherwise
// produce a ref update cannot address.
func refChannel(echoed, configured string) string {
	if echoed != "" {
		return echoed
	}
	return configured
}

// call bundles one Slack request. Method and Channel are for error text only —
// neither the endpoint URL nor the token may ever appear in an error, which a
// pinned test asserts.
type call struct {
	client   *http.Client
	endpoint string
	token    string
	method   string
	channel  string
	payload  any
}

// callSlack is the one Slack transport: POST a JSON payload with the bot token,
// require HTTP 200 AND "ok": true (Slack reports API errors inside 200 bodies),
// and return the decoded response. Mechanism only — what to say is decided by
// the renderers above it.
func callSlack(c call) (slackResponse, error) {
	body, err := json.Marshal(c.payload)
	if err != nil {
		return slackResponse{}, c.errf("encode message: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return slackResponse{}, c.errf("build request: %w", requestCause(err))
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return slackResponse{}, c.errf("request: %w", requestCause(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return slackResponse{}, c.errf("status %s", resp.Status)
	}
	var result slackResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return slackResponse{}, c.errf("decode response: %w", err)
	}
	if !result.OK {
		return slackResponse{}, c.errf("API error %q", result.Error)
	}
	return result, nil
}

// errf renders one transport failure: the channel and the Slack method, never
// the endpoint or the token. The prefix is inlined into the caller's format so
// a wrapped cause keeps its natural depth — an extra fmt.Errorf around it would
// add an Unwrap level for nothing.
func (c call) errf(format string, args ...any) error {
	return fmt.Errorf("notify: slack channel %q: %s: "+format,
		append([]any{c.channel, c.method}, args...)...)
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
	if !resolveActions {
		return elements
	}
	if grantApprovalRequest(ev) {
		return append(elements, grantApproveButton(ev), grantDenyButton(ev))
	}
	if !resolvablePark(ev) {
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

func grantApprovalRequest(ev event.Event) bool {
	return ev.Kind == "escalation" && ev.ID != "" && ev.Fields["grant_request"] == "yes"
}

func grantApproveButton(ev event.Event) slackButton {
	return slackButton{
		Type: "button", Text: slackText{Type: "plain_text", Text: "Approve T0", Emoji: true},
		ActionID: grantrequest.ActionApprove, Value: ev.ID, Style: "primary",
	}
}

func grantDenyButton(ev event.Event) slackButton {
	return slackButton{
		Type: "button", Text: slackText{Type: "plain_text", Text: "Deny", Emoji: true},
		ActionID: grantrequest.ActionDeny, Value: ev.ID, Style: "danger",
	}
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
	if grantApprovalRequest(ev) {
		if s := subject(ev); s != "" {
			return "🔐 Approve exact T0 access for " + s + "?"
		}
		return "🔐 Approve exact T0 access?"
	}
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
	//
	// Deliberately NOT gated on resolvablePark. A ceiling park is excluded from
	// that check — it has no tap to offer — but the pre-flight still proves its
	// approval cannot land, and its body already says "Cannot approve". Gating
	// here would leave exactly that card headed "Your call", which is the
	// dishonest half of the pair.
	if !approvable(ev) {
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
	var lines []string
	// Each line is guarded on its own content: a label with nothing after it is
	// worse than no label, and this block renders on any card the pre-flight
	// refused, not only the ones it filled in completely.
	if blocker := compact(ev.Fields["blocker"]); blocker != "" {
		lines = append(lines, "*Cannot approve:* "+blocker)
	}
	if needs := compact(ev.Fields["needs"]); needs != "" {
		lines = append(lines, "*Needs:* "+needs)
	}
	if mint := compact(ev.Fields["mint"]); mint != "" {
		lines = append(lines, "```"+mint+"```")
	}
	if len(lines) == 0 {
		return ""
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

// --- the finalized card ------------------------------------------------------
//
// A card is a snapshot, and a snapshot of a parked escalation is stale the
// moment the park resolves. gate's inbox reduces parked runs by subject, so a
// re-park, a merge, or a keyboard `gate judge` drops an older park out of the
// inbox — while its Approve button stays live in Slack. Two taps died that way
// on 2026-08-21 ("escalation is not currently parked: esc_… not in gate
// inbox"), which reads as a broken tool rather than a card that moved on.
//
// So a resolved park gets its card rewritten: buttons off, outcome and decider
// on, authorized head sha when the run reached one. This is still rendering,
// not deciding — every fact below was recorded by gate.

// renderFinalCard rewrites a delivered card as its terminal state. The actions
// row keeps only the View PR link: the resolve buttons are exactly what must
// not survive, since the park behind them is gone.
func renderFinalCard(ref Ref, ev event.Event) slackUpdate {
	blocks := []slackBlock{
		{Type: "header", Text: &slackText{Type: "plain_text", Text: finalHeadline(ev), Emoji: true}},
	}
	if body := finalBody(ev); body != "" {
		blocks = append(blocks, slackBlock{Type: "section", Text: &slackText{Type: "mrkdwn", Text: body}})
	}
	if btn, ok := prButton(ev); ok {
		blocks = append(blocks, slackBlock{Type: "actions", Elements: []any{btn}})
	}
	blocks = append(blocks, slackBlock{
		Type:     "context",
		Elements: []any{slackText{Type: "mrkdwn", Text: slackFooter(ev)}},
	})
	return slackUpdate{
		Channel:     ref.ChannelID,
		TS:          ref.TS,
		Text:        outcomeLine(ev),
		Attachments: []slackAttachment{{Color: finalColor(ev), Fallback: outcomeLine(ev), Blocks: blocks}},
	}
}

func finalHeadline(ev event.Event) string {
	s := subject(ev)
	if s != "" {
		s = " — " + s
	}
	switch ev.Fields["outcome"] {
	case event.OutcomeApproved:
		return "✅ Approved" + s
	case event.OutcomeBlocked:
		return "⛔ Blocked" + s
	case event.OutcomeMergeAuthorized:
		return "🏁 Merge authorized" + s
	case event.OutcomeSuperseded:
		return "🕓 Superseded by a newer run" + s
	}
	return "☑️ Resolved" + s
}

// finalColor drains a resolved card of urgency: green for a decision that let
// the work proceed, grey for everything else. A resolved card that keeps its
// escalate-orange still reads as something to do.
func finalColor(ev event.Event) string {
	if ev.Fields["outcome"] == event.OutcomeApproved || ev.Fields["outcome"] == event.OutcomeMergeAuthorized {
		return "#2E7D32"
	}
	return "#9AA0A6"
}

// finalBody states who decided and what the decision covers. The sha is labeled
// AUTHORIZED, not merged: gate's action records the merge it authorizes and the
// exact head it pins, but the merge is run by the caller and gate keeps no
// receipt that one landed. Claiming "merged" would assert a fact nothing wrote.
func finalBody(ev event.Event) string {
	var lines []string
	if who := compact(ev.Fields["who"]); who != "" {
		lines = append(lines, "*Decided by:* "+who)
	}
	if sha := compact(ev.Fields["sha"]); sha != "" {
		lines = append(lines, "*Authorized head:* `"+sha+"`")
	}
	if note := compact(ev.Fields["note"]); note != "" {
		lines = append(lines, note)
	}
	if len(lines) == 0 {
		return ""
	}
	return truncateRunes(strings.Join(lines, "\n"), slackSectionLimit)
}

// outcomeLine is the terminal receipt posted into the card's thread. Today a
// successful tap and a silently failed one look identical once the ack fades;
// this line is the difference. It is also the card's notification text, so the
// resolution reaches a phone that never opens the thread.
func outcomeLine(ev event.Event) string {
	who := compact(ev.Fields["who"])
	if who == "" {
		who = "gate"
	}
	s := subject(ev)
	switch ev.Fields["outcome"] {
	case event.OutcomeApproved:
		return approvedLine(who, s, compact(ev.Fields["sha"]))
	case event.OutcomeBlocked:
		return fmt.Sprintf("⛔ Blocked by %s — %s will not be merged under this run.", who, orThisPark(s))
	case event.OutcomeMergeAuthorized:
		return fmt.Sprintf("🏁 Merge authorized for %s at `%s`.", orThisPark(s), compact(ev.Fields["sha"]))
	case event.OutcomeSuperseded:
		return fmt.Sprintf("🕓 Superseded — a newer gate run parked %s, so this card's buttons no longer resolve anything.", orThisPark(s))
	}
	return fmt.Sprintf("☑️ Resolved by %s — %s is no longer awaiting judgment.", who, orThisPark(s))
}

func approvedLine(who, subject, sha string) string {
	if sha == "" {
		return fmt.Sprintf("✅ Approved by %s — %s recorded; gate has not authorized a merge for it.", who, orThisPark(subject))
	}
	return fmt.Sprintf("✅ Approved by %s — gate authorized the merge of %s at `%s`.", who, orThisPark(subject), sha)
}

func orThisPark(subject string) string {
	if subject == "" {
		return "this park"
	}
	return subject
}
