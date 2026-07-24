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
		return slack(ch.Token, ch.ChannelID, ev)
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

type slackButton struct {
	Type  string    `json:"type"`
	Text  slackText `json:"text"`
	URL   string    `json:"url,omitempty"`
	Style string    `json:"style,omitempty"`
}

type slackResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

func slack(token, channel string, ev event.Event) error {
	client := &http.Client{Timeout: 15 * time.Second}
	return postSlack(client, slackPostMessageURL, token, channel, ev)
}

func postSlack(client *http.Client, endpoint, token, channel string, ev event.Event) error {
	body, err := json.Marshal(renderSlackMessage(channel, ev))
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
func renderSlackMessage(channel string, ev event.Event) slackRequest {
	return slackRequest{
		Channel: channel,
		Attachments: []slackAttachment{{
			Color:    severityColor(ev.Severity),
			Fallback: slackFallback(ev),
			Blocks:   slackBlocks(ev),
		}},
	}
}

func slackBlocks(ev event.Event) []slackBlock {
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
	if btn, ok := prButton(ev); ok {
		blocks = append(blocks, slackBlock{Type: "actions", Elements: []any{btn}})
	}
	footer := slackText{Type: "mrkdwn", Text: slackFooter(ev)}
	return append(blocks, slackBlock{Type: "context", Elements: []any{footer}})
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
	if s := subject(ev); s != "" {
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
	why := compact(ev.Fields["brief_concern"])
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
