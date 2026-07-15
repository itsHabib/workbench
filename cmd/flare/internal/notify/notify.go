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
// is unmistakable. Text is the notification/preview fallback (lock screen).
type slackRequest struct {
	Channel     string            `json:"channel"`
	Text        string            `json:"text"`
	Attachments []slackAttachment `json:"attachments,omitempty"`
}

type slackAttachment struct {
	Color  string       `json:"color"`
	Blocks []slackBlock `json:"blocks"`
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

// renderSlackMessage turns an event into a Block Kit message: a colored
// attachment whose blocks lead on the required action.
func renderSlackMessage(channel string, ev event.Event) slackRequest {
	return slackRequest{
		Channel:     channel,
		Text:        slackFallback(ev),
		Attachments: []slackAttachment{{Color: severityColor(ev.Severity), Blocks: slackBlocks(ev)}},
	}
}

func slackBlocks(ev event.Event) []slackBlock {
	blocks := []slackBlock{
		{Type: "header", Text: &slackText{Type: "plain_text", Text: headline(ev), Emoji: true}},
		{Type: "section", Text: &slackText{Type: "mrkdwn", Text: detailLine(ev)}},
	}
	if why := compact(ev.Body); why != "" {
		quote := "> " + truncateRunes(why, slackSectionLimit)
		blocks = append(blocks, slackBlock{Type: "section", Text: &slackText{Type: "mrkdwn", Text: quote}})
	}
	if btn, ok := prButton(ev); ok {
		blocks = append(blocks, slackBlock{Type: "actions", Elements: []any{btn}})
	}
	footer := slackText{Type: "mrkdwn", Text: slackFooter(ev)}
	return append(blocks, slackBlock{Type: "context", Elements: []any{footer}})
}

// headline is the one line that must make the required action obvious: a
// severity verb, plus the subject ("rooms#71") when the event carries one.
func headline(ev event.Event) string {
	h := severityHeadline(ev.Severity)
	if s := subject(ev); s != "" {
		h += " · " + s
	}
	return h
}

func severityHeadline(s event.Severity) string {
	switch s {
	case event.SevBlock:
		return ":octagonal_sign: Blocked — needs you"
	case event.SevEscalate:
		return ":warning: Needs your judgment"
	case event.SevFailed:
		return ":x: Run failed"
	case event.SevCancelled:
		return ":white_circle: Run cancelled"
	}
	return ":information_source: Notice"
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

// detailLine is the "what / where" context: the source, then the meaningful
// fields the producer surfaced, in a stable order.
func detailLine(ev event.Event) string {
	parts := []string{"*" + ev.Source + "*"}
	for _, k := range []string{"decision", "outcome", "dimension", "tier", "task", "run"} {
		v := ev.Fields[k]
		if v == "" {
			continue
		}
		parts = append(parts, slackFieldValue(k, v))
	}
	return strings.Join(parts, "  ·  ")
}

func slackFieldValue(key, val string) string {
	switch key {
	case "tier":
		return "tier `" + val + "`"
	case "run", "task":
		return "`" + val + "`"
	}
	return val
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

func slackFooter(ev event.Event) string {
	return ":stopwatch: " + ev.Time.Format("Jan 2, 3:04 PM MST") + "  ·  " + ev.Severity.String()
}

// slackFallback is the notification/preview text (the lock-screen line): the
// action first, then the reason, capped to Slack's text limit.
func slackFallback(ev event.Event) string {
	t := headline(ev)
	if why := compact(ev.Body); why != "" {
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
