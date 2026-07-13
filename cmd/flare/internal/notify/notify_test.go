package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/itsHabib/workbench/cmd/flare/internal/event"
)

func TestSlackPostRendersEvent(t *testing.T) {
	const token = "test-token"
	const channel = "C123"
	var got slackRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer "+token {
			t.Errorf("Authorization = %q", auth)
		}
		if contentType := r.Header.Get("Content-Type"); contentType != "application/json" {
			t.Errorf("Content-Type = %q", contentType)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	ev := event.Event{
		Source:   "gate",
		ID:       "evt-1",
		Kind:     "verdict",
		Time:     time.Now(),
		Severity: event.SevBlock,
		Title:    "gate: workbench#33 blocked",
		Body:     "review found a critical issue\nthat needs judgment",
	}
	if err := postSlack(server.Client(), server.URL, token, channel, ev); err != nil {
		t.Fatal(err)
	}
	if got.Channel != channel {
		t.Fatalf("channel = %q, want %q", got.Channel, channel)
	}
	want := "[block] gate: workbench#33 blocked — review found a critical issue that needs judgment"
	if got.Text != want {
		t.Fatalf("text = %q, want %q", got.Text, want)
	}
}

func TestSlackAPIFailureIsAnError(t *testing.T) {
	const token = "test-token"
	const channel = "C123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_payload"}`))
	}))
	defer server.Close()

	err := postSlack(server.Client(), server.URL, token, channel, event.Event{Source: "gate"})
	assertSafeSlackError(t, err, token, server.URL, channel, "invalid_payload")
}

func TestSlackNetworkFailureIsAnError(t *testing.T) {
	const token = "test-token"
	const channel = "C123"
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := server.Client()
	endpoint := server.URL
	server.Close()

	err := postSlack(client, endpoint, token, channel, event.Event{Source: "gate"})
	assertSafeSlackError(t, err, token, endpoint, channel, "request")
}

func TestSlackBuildRequestFailureIsSafe(t *testing.T) {
	const token = "test-token"
	const channel = "C123"
	const endpoint = "://secret-endpoint"

	err := postSlack(http.DefaultClient, endpoint, token, channel, event.Event{Source: "gate"})
	assertSafeSlackError(t, err, token, endpoint, channel, "build request")
}

func TestSlackTextIsTruncated(t *testing.T) {
	text := renderSlackText(event.Event{
		Source:   "gate",
		Severity: event.SevEscalate,
		Title:    "gate: parked",
		Body:     strings.Repeat("界", slackTextLimit),
	})
	if got := utf8.RuneCountInString(text); got != slackTextLimit {
		t.Fatalf("rune count = %d, want %d", got, slackTextLimit)
	}
	if !strings.HasSuffix(text, "…") {
		t.Fatalf("truncated text must end in ellipsis: %q", text[len(text)-10:])
	}
}

func TestSlackTextWithoutDetailHasNoTrailingSeparator(t *testing.T) {
	text := renderSlackText(event.Event{Source: "gate", Severity: event.SevInfo})
	if text != "[info] gate" {
		t.Fatalf("text = %q, want %q", text, "[info] gate")
	}
}

func assertSafeSlackError(t *testing.T, err error, token, endpoint, channel, want string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error")
	}
	for _, secret := range []string{token, endpoint} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks secret or endpoint %q: %v", secret, err)
		}
	}
	for _, substring := range []string{channel, want} {
		if !strings.Contains(err.Error(), substring) {
			t.Fatalf("error = %v, want %q", err, substring)
		}
	}
}
