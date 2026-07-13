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
	slackTextLimit      = 4000
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

type slackRequest struct {
	Channel string `json:"channel"`
	Text    string `json:"text"`
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
	body, err := json.Marshal(slackRequest{Channel: channel, Text: renderSlackText(ev)})
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

func renderSlackText(ev event.Event) string {
	source := compact(ev.Source)
	title := compact(ev.Title)
	title = strings.TrimSpace(strings.TrimPrefix(title, source+":"))
	detail := title
	why := compact(ev.Body)
	if detail == "" {
		detail = why
		why = ""
	}
	if why != "" && why != detail {
		detail += " — " + why
	}
	text := fmt.Sprintf("[%s]", ev.Severity.String())
	if source != "" {
		text += " " + source
	}
	if detail != "" {
		text += ": " + detail
	}
	return truncateSlackText(text)
}

func compact(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func truncateSlackText(s string) string {
	if utf8.RuneCountInString(s) <= slackTextLimit {
		return s
	}
	runes := []rune(s)
	return string(runes[:slackTextLimit-1]) + "…"
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
