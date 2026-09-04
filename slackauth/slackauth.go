// Package slackauth implements the shared mechanism for authenticating and
// parsing one Slack interactive-action callback. It contains no Gate or
// Escalate policy: callers decide which users and action ids they authorize.
package slackauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// SignatureHeader and TimestampHeader are Slack's v0 authentication headers.
	SignatureHeader = "X-Slack-Signature"
	// TimestampHeader carries the epoch second included in Slack's signature.
	TimestampHeader = "X-Slack-Request-Timestamp"
	// MaxSkew is Slack's recommended replay window.
	MaxSkew = 5 * time.Minute
	// MaxBody bounds callback memory before authentication and parsing.
	MaxBody = 1 << 20
)

// Interaction is the small verified payload both callback consumers need.
// UserID is Slack's immutable identity; Username and Name are presentation.
type Interaction struct {
	UserID      string
	Username    string
	Name        string
	ActionID    string
	Value       string
	ResponseURL string
}

// Actor renders a readable audit identity while always retaining immutable id.
func (i Interaction) Actor() string {
	handle := i.Username
	if handle == "" {
		handle = i.Name
	}
	if handle == "" {
		return i.UserID
	}
	if i.UserID == "" {
		return "@" + handle
	}
	return "@" + handle + " (" + i.UserID + ")"
}

// Verify authenticates the raw body using Slack's v0 HMAC and freshness rule.
func Verify(secret []byte, signature, timestamp string, body []byte, now time.Time) error {
	if len(secret) == 0 {
		return errors.New("slackauth: no signing secret configured")
	}
	if signature == "" || timestamp == "" {
		return errors.New("slackauth: missing signature headers")
	}
	if len(body) > MaxBody {
		return fmt.Errorf("slackauth: body exceeds %d bytes", MaxBody)
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("slackauth: bad timestamp %q: %w", timestamp, err)
	}
	skew := now.UTC().Sub(time.Unix(seconds, 0).UTC())
	if skew > MaxSkew || skew < -MaxSkew {
		return fmt.Errorf("slackauth: stale timestamp: skew %s exceeds %s", skew, MaxSkew)
	}
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "v0:%s:%s", timestamp, body)
	want := "v0=" + hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(signature)) {
		return errors.New("slackauth: signature mismatch")
	}
	return nil
}

// Parse decodes one form-encoded interactive payload after verification.
func Parse(body []byte) (Interaction, error) {
	if len(body) > MaxBody {
		return Interaction{}, fmt.Errorf("slackauth: body exceeds %d bytes", MaxBody)
	}
	form, err := url.ParseQuery(string(body))
	if err != nil {
		return Interaction{}, fmt.Errorf("slackauth: parse form: %w", err)
	}
	raw := form.Get("payload")
	if raw == "" {
		return Interaction{}, errors.New("slackauth: no payload field")
	}
	var payload struct {
		Type string `json:"type"`
		User struct {
			ID       string `json:"id"`
			Username string `json:"username"`
			Name     string `json:"name"`
		} `json:"user"`
		Actions []struct {
			ActionID string `json:"action_id"`
			Value    string `json:"value"`
		} `json:"actions"`
		ResponseURL string `json:"response_url"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return Interaction{}, fmt.Errorf("slackauth: decode payload: %w", err)
	}
	if payload.Type != "block_actions" {
		return Interaction{}, fmt.Errorf("slackauth: unsupported payload type %q", payload.Type)
	}
	if len(payload.Actions) != 1 {
		return Interaction{}, fmt.Errorf("slackauth: payload must carry exactly one action, got %d", len(payload.Actions))
	}
	action := payload.Actions[0]
	if payload.User.ID == "" || action.ActionID == "" || action.Value == "" {
		return Interaction{}, errors.New("slackauth: user id, action id, and value are required")
	}
	return Interaction{
		UserID: payload.User.ID, Username: payload.User.Username, Name: payload.User.Name,
		ActionID: action.ActionID, Value: action.Value,
		ResponseURL: payload.ResponseURL,
	}, nil
}

// Authenticate verifies before parsing so unauthenticated input never reaches
// the structured callback path.
func Authenticate(secret []byte, signature, timestamp string, body []byte, now time.Time) (Interaction, error) {
	if err := Verify(secret, signature, timestamp, body, now); err != nil {
		return Interaction{}, err
	}
	return Parse(body)
}

// AllowUsers reports membership in an immutable Slack-user-id allowlist.
func AllowUsers(ids []string, userID string) bool {
	if userID == "" {
		return false
	}
	for _, id := range ids {
		if strings.TrimSpace(id) == userID {
			return true
		}
	}
	return false
}
