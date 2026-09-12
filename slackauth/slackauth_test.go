package slackauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAuthenticateAndActor(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	body := callbackBody(`{"id":"U123","username":"operator"}`, `[{"action_id":"gate_t0_approve","value":"gqr_abcd"}]`)
	ts := strconv.FormatInt(now.Unix(), 10)
	interaction, err := Authenticate([]byte("secret"), sign("secret", ts, body), ts, body, now)
	if err != nil {
		t.Fatal(err)
	}
	if interaction.UserID != "U123" || interaction.ActionID != "gate_t0_approve" || interaction.Value != "gqr_abcd" {
		t.Fatalf("interaction = %+v", interaction)
	}
	if interaction.Actor() != "@operator (U123)" {
		t.Fatalf("actor = %q", interaction.Actor())
	}
	if !AllowUsers([]string{"U999", " U123 "}, interaction.UserID) {
		t.Fatal("expected immutable id in allowlist")
	}
}

func TestAuthenticateRefusesBeforeParse(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	body := []byte("not a form")
	ts := strconv.FormatInt(now.Unix(), 10)
	if _, err := Authenticate([]byte("secret"), "v0=bad", ts, body, now); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("bad signature = %v", err)
	}
	if _, err := Authenticate([]byte("secret"), sign("secret", ts, body), ts, body, now); err == nil || !strings.Contains(err.Error(), "payload") {
		t.Fatalf("malformed authenticated body = %v", err)
	}
}

func TestVerifyRefusesReplayAndOversize(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	body := callbackBody(`{"id":"U123"}`, `[{"action_id":"approve","value":"esc_abcd"}]`)
	stale := strconv.FormatInt(now.Add(-MaxSkew-time.Second).Unix(), 10)
	if err := Verify([]byte("secret"), sign("secret", stale, body), stale, body, now); err == nil {
		t.Fatal("expected stale callback refusal")
	}
	large := make([]byte, MaxBody+1)
	ts := strconv.FormatInt(now.Unix(), 10)
	if err := Verify([]byte("secret"), sign("secret", ts, large), ts, large, now); err == nil {
		t.Fatal("expected oversized body refusal")
	}
}

func TestParseRequiresOneCompleteAction(t *testing.T) {
	tests := [][]byte{
		[]byte(url.Values{"payload": {`{"type":"view_submission","user":{"id":"U123"},"actions":[{"action_id":"a","value":"v"}]}`}}.Encode()),
		callbackBody(`{"id":"U123"}`, `[]`),
		callbackBody(`{"id":"U123"}`, `[{"action_id":"a","value":"v"},{"action_id":"b","value":"v"}]`),
		callbackBody(`{"id":""}`, `[{"action_id":"a","value":"v"}]`),
		callbackBody(`{"id":"U123"}`, `[{"action_id":"","value":"v"}]`),
	}
	for _, body := range tests {
		if _, err := Parse(body); err == nil {
			t.Fatalf("expected refusal for %s", body)
		}
	}
}

func callbackBody(user, actions string) []byte {
	payload := fmt.Sprintf(`{"type":"block_actions","user":%s,"actions":%s,"response_url":"https://hooks.slack.com/actions/1"}`, user, actions)
	return []byte(url.Values{"payload": {payload}}.Encode())
}

func sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "v0:%s:%s", timestamp, body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}
