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

func TestSlackPostRendersBlockKit(t *testing.T) {
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
		Fields:   map[string]string{"decision": "block", "repo": "itsHabib/workbench", "number": "33"},
	}
	if err := postSlack(server.Client(), server.URL, token, channel, ev); err != nil {
		t.Fatal(err)
	}
	if got.Channel != channel {
		t.Fatalf("channel = %q, want %q", got.Channel, channel)
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("want one attachment, got %d", len(got.Attachments))
	}
	if c := got.Attachments[0].Color; c != severityColor(event.SevBlock) {
		t.Fatalf("attachment color = %q, want %q", c, severityColor(event.SevBlock))
	}
	blocks := got.Attachments[0].Blocks
	if len(blocks) == 0 || blocks[0].Type != "header" || blocks[0].Text == nil {
		t.Fatalf("first block must be a header, got %+v", blocks)
	}
	if h := blocks[0].Text.Text; !strings.Contains(h, "Don't merge") || !strings.Contains(h, "workbench#33") {
		t.Fatalf("header must lead on the action and name the subject, got %q", h)
	}
	if !hasSectionContaining(blocks, "critical issue") {
		t.Fatalf("the why must appear in a section, got %+v", blocks)
	}
	if fb := got.Attachments[0].Fallback; !strings.Contains(fb, "Don't merge") {
		t.Fatalf("notification fallback must lead on the action, got %q", fb)
	}
}

func hasSectionContaining(blocks []slackBlock, sub string) bool {
	for _, b := range blocks {
		if b.Text != nil && strings.Contains(b.Text.Text, sub) {
			return true
		}
	}
	return false
}

func TestSlackMessageRendersOnce(t *testing.T) {
	// The blocks live inside the attachment and there is no top-level text, so
	// Slack renders the card exactly once — not a summary line stacked above a
	// card that repeats it. The notification line lives on the fallback.
	msg := renderSlackMessage("C1", event.Event{
		Source:   "gate",
		Severity: event.SevBlock,
		Body:     "tier over ceiling",
		Fields:   map[string]string{"repo": "itsHabib/rooms", "number": "71"},
	})
	blob, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(blob, &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["text"]; ok {
		t.Fatalf("a top-level text stacks a duplicate above the card; want none:\n%s", blob)
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0].Fallback == "" {
		t.Fatalf("the notification line must live on the attachment fallback, got %+v", msg.Attachments)
	}
}

// TestSlackEscalationHasPRButton pins the acceptance for the escalation
// click-target: an escalation naming a PR renders the same View PR button and
// header subject verdicts get.
func TestSlackEscalationHasPRButton(t *testing.T) {
	msg := renderSlackMessage("C1", event.Event{
		Source:   "gate",
		Kind:     "escalation",
		Severity: event.SevEscalate,
		Body:     "your call",
		Fields: map[string]string{
			"run": "run_7", "repo": "itsHabib/workbench", "number": "64",
		},
	})
	blob, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(blob)
	for _, want := range []string{
		`"url":"https://github.com/itsHabib/workbench/pull/64"`,
		"View PR #64",
		"workbench#64",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("escalation message missing %q\n%s", want, s)
		}
	}
}

func TestSlackVerdictHasPRButton(t *testing.T) {
	msg := renderSlackMessage("C1", event.Event{
		Source:   "gate",
		Kind:     "verdict",
		Severity: event.SevEscalate,
		Body:     "tier over ceiling",
		Fields: map[string]string{
			"decision": "escalate", "repo": "itsHabib/rooms", "number": "71",
			"tier": "T0", "dimension": "reducer", "run": "run_9",
		},
	})
	blob, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(blob)
	for _, want := range []string{
		`"url":"https://github.com/itsHabib/rooms/pull/71"`,
		"View PR #71",
		"rooms#71", // header subject, owner stripped
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("verdict message missing %q\n%s", want, s)
		}
	}
}

func TestSlackEscalationHasNoButton(t *testing.T) {
	msg := renderSlackMessage("C1", event.Event{
		Source:   "gate",
		Kind:     "escalation",
		Severity: event.SevEscalate,
		Body:     "needs judgment",
		Fields:   map[string]string{"run": "run_1"},
	})
	blob, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(blob)
	if strings.Contains(s, `"type":"button"`) {
		t.Fatalf("an escalation with no PR must carry no button:\n%s", s)
	}
	if !strings.Contains(s, "Your call") {
		t.Fatalf("escalation header must lead on the action:\n%s", s)
	}
}

func TestSlackFallbackLeadsWithActionAndTruncates(t *testing.T) {
	lead := slackFallback(event.Event{Source: "gate", Severity: event.SevBlock})
	if !strings.HasPrefix(lead, "🛑") {
		t.Fatalf("fallback must lead with the severity action, got %q", lead)
	}
	long := slackFallback(event.Event{
		Source:   "gate",
		Severity: event.SevEscalate,
		Body:     strings.Repeat("界", slackTextLimit),
	})
	if got := utf8.RuneCountInString(long); got > slackTextLimit {
		t.Fatalf("fallback rune count = %d, want <= %d", got, slackTextLimit)
	}
	if !strings.HasSuffix(long, "…") {
		t.Fatalf("a truncated fallback must end in an ellipsis, got %q", long[len(long)-6:])
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
