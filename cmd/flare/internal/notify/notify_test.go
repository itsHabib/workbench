package notify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/itsHabib/workbench/cmd/flare/internal/event"
	"github.com/itsHabib/workbench/contracts/escalation"
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
	if err := postSlack(server.Client(), server.URL, token, channel, false, ev); err != nil {
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
	msg := renderSlackMessage("C1", false, event.Event{
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
	msg := renderSlackMessage("C1", false, event.Event{
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
	msg := renderSlackMessage("C1", false, event.Event{
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
	msg := renderSlackMessage("C1", false, event.Event{
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

func TestSlackShipParkIsNotAHumanPolicyDecision(t *testing.T) {
	msg := renderSlackMessage("C1", true, event.Event{
		Source:   "ship",
		Kind:     "receipt",
		Severity: event.SevFailed,
		Time:     time.Date(2026, 7, 28, 22, 20, 0, 0, time.UTC),
		Title:    "ship: demo parked",
		Body:     "run demo paused after a failure or dispatch ambiguity",
		Fields:   map[string]string{"outcome": "parked", "repo": "itsHabib/workbench", "number": "164"},
	})
	blob, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	s := string(blob)
	if strings.Contains(s, "Your call") {
		t.Fatalf("a Ship mechanism park must not render a policy-decision headline:\n%s", s)
	}
	if !strings.Contains(s, "Run parked") {
		t.Fatalf("a Ship mechanism park must describe the actual parked state:\n%s", s)
	}
	if strings.Contains(s, "Run failed") {
		t.Fatalf("a Ship mechanism park must not be mislabeled as failed:\n%s", s)
	}
	approveID := fmt.Sprintf(`"action_id":%q`, escalation.ActionApprove)
	blockID := fmt.Sprintf(`"action_id":%q`, escalation.ActionBlock)
	if strings.Contains(s, approveID) || strings.Contains(s, blockID) {
		t.Fatalf("a Ship mechanism park must not render Gate resolve actions:\n%s", s)
	}
}

// TestSlackEscalationRendersBriefSections pins the zero-context page: an
// escalation carrying gate's synthesized brief renders labeled
// What/Concern/Risk/Recommendation sections instead of quoting the raw
// machine reason, and the lock-screen fallback leads with the concern.
func TestSlackEscalationRendersBriefSections(t *testing.T) {
	ev := event.Event{
		Source:   "gate",
		Kind:     "escalation",
		Severity: event.SevEscalate,
		Body:     "1 bot comments: 1 actionable, 0 low-confidence extractions — needs judgment",
		Fields: map[string]string{
			"run": "run_8", "repo": "itsHabib/rooms", "number": "84",
			"brief_what":    "A design spec for a test harness meant to prove a microVM can't leak secrets.",
			"brief_concern": "The harness's own pass/fail check is broken, so the test could show contained even if secrets escaped.",
			"brief_risk":    "Medium — it's a spec, not shipping code.",
			"brief_rec":     "Have the author fix the witness before merging.",
		},
	}
	blob, err := json.Marshal(renderSlackMessage("C1", false, ev))
	if err != nil {
		t.Fatal(err)
	}
	s := string(blob)
	for _, want := range []string{
		"*What it is:* A design spec",
		"*The concern:* The harness's own pass/fail check is broken",
		"*Risk:* Medium",
		"*Recommendation:* Have the author fix the witness",
		"View PR #84", // brief must not displace the click-target
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("brief card missing %q\n%s", want, s)
		}
	}
	if strings.Contains(s, "low-confidence extractions") {
		t.Fatalf("with a brief, the raw tally must not render on the card:\n%s", s)
	}
	if fb := slackFallback(ev); !strings.Contains(fb, "pass/fail check is broken") {
		t.Fatalf("fallback must lead with the concern, got %q", fb)
	}

	// No brief: the card quotes the producer's reason exactly as before.
	ev.Fields = map[string]string{"run": "run_8", "repo": "itsHabib/rooms", "number": "84"}
	blob, err = json.Marshal(renderSlackMessage("C1", false, ev))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), "needs judgment") {
		t.Fatalf("brief-less escalation must still quote the reason:\n%s", blob)
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

	err := postSlack(server.Client(), server.URL, token, channel, false, event.Event{Source: "gate"})
	assertSafeSlackError(t, err, token, server.URL, channel, "invalid_payload")
}

func TestSlackNetworkFailureIsAnError(t *testing.T) {
	const token = "test-token"
	const channel = "C123"
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	client := server.Client()
	endpoint := server.URL
	server.Close()

	err := postSlack(client, endpoint, token, channel, false, event.Event{Source: "gate"})
	assertSafeSlackError(t, err, token, endpoint, channel, "request")
}

func TestSlackBuildRequestFailureIsSafe(t *testing.T) {
	const token = "test-token"
	const channel = "C123"
	const endpoint = "://secret-endpoint"

	err := postSlack(http.DefaultClient, endpoint, token, channel, false, event.Event{Source: "gate"})
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

// resolveButtons collects every interactive resolve button (one carrying an
// action_id) across the card's blocks, so a test can assert on the exact
// Approve/Block set flare rendered rather than substring-matching JSON.
func resolveButtons(blocks []slackBlock) []slackButton {
	var out []slackButton
	for _, b := range blocks {
		if b.Type != "actions" {
			continue
		}
		for _, el := range b.Elements {
			btn, ok := el.(slackButton)
			if ok && btn.ActionID != "" {
				out = append(out, btn)
			}
		}
	}
	return out
}

// TestSlackResolveButtonsRenderOnOptIn pins the Phase-2 acceptance: with the
// channel opted in, a briefed parked escalation renders Approve and Block
// interactive buttons carrying the SHARED action-id vocabulary and the
// escalation id as their value — the exact shape escalate's serve parses — and
// still keeps the View PR link. The buttons are rendered by value comparison,
// not JSON substrings, so a drift in field names fails loudly.
func TestSlackResolveButtonsRenderOnOptIn(t *testing.T) {
	msg := renderSlackMessage("C1", true, event.Event{
		Source:   "gate",
		ID:       "esc-abc123",
		Kind:     "escalation",
		Severity: event.SevEscalate,
		Body:     "your call",
		Fields: map[string]string{
			"run": "run_7", "repo": "itsHabib/workbench", "number": "137",
			"brief_what": "escalate serve", "briefed": "yes",
		},
	})
	btns := resolveButtons(msg.Attachments[0].Blocks)
	if len(btns) != 2 {
		t.Fatalf("want exactly Approve+Block resolve buttons, got %d: %+v", len(btns), btns)
	}
	approve, block := btns[0], btns[1]
	if approve.ActionID != escalation.ActionApprove || approve.Style != "primary" {
		t.Fatalf("approve button = %+v, want action_id %q style primary", approve, escalation.ActionApprove)
	}
	if block.ActionID != escalation.ActionBlock || block.Style != "danger" {
		t.Fatalf("block button = %+v, want action_id %q style danger", block, escalation.ActionBlock)
	}
	for _, b := range btns {
		if b.Value != "esc-abc123" {
			t.Fatalf("button value = %q, want the escalation id the callback joins on", b.Value)
		}
		if b.URL != "" {
			t.Fatalf("an interactive button must carry no url (Slack routes it to the app), got %q", b.URL)
		}
	}
	// The click-target link survives beside the resolve buttons.
	if !strings.Contains(string(mustJSON(t, msg)), "View PR #137") {
		t.Fatalf("resolve buttons must not displace the View PR link:\n%s", mustJSON(t, msg))
	}
}

// TestSlackResolveButtonsRequireOptIn pins that the buttons are DARK by default:
// the very same parked escalation on a channel that has not opted in renders no
// resolve buttons (a tap would be dead until the Slack app's Request URL is
// wired), while the View PR link still renders.
func TestSlackResolveButtonsRequireOptIn(t *testing.T) {
	ev := event.Event{
		Source:   "gate",
		ID:       "esc-abc123",
		Kind:     "escalation",
		Severity: event.SevEscalate,
		Body:     "your call",
		Fields:   map[string]string{"repo": "itsHabib/workbench", "number": "137"},
	}
	msg := renderSlackMessage("C1", false, ev)
	if btns := resolveButtons(msg.Attachments[0].Blocks); len(btns) != 0 {
		t.Fatalf("resolve buttons must be off unless the channel opts in, got %+v", btns)
	}
	if !strings.Contains(string(mustJSON(t, msg)), "View PR #137") {
		t.Fatalf("opt-out must not drop the View PR link:\n%s", mustJSON(t, msg))
	}
}

// TestSlackResolveButtonsOnlyOnResolvableParks pins the correctness guard: even
// opted in, resolve buttons render ONLY for a gate park (Kind "escalation") that
// carries its artifact id. The other things that reach SevEscalate — a verdict
// with an escalate decision, a cursor-alert, a park missing its id — are not
// resolvable by `escalate`, so offering Approve/Block on them would be a tap
// gate would refuse.
func TestSlackResolveButtonsOnlyOnResolvableParks(t *testing.T) {
	cases := []struct {
		name string
		ev   event.Event
	}{
		{"verdict-escalate", event.Event{
			Source: "gate", ID: "v1", Kind: "verdict", Severity: event.SevEscalate,
			Fields: map[string]string{"decision": "escalate", "repo": "itsHabib/rooms", "number": "71"},
		}},
		{"cursor-alert", event.Event{
			Source: "gate", ID: "cursor-alert:gate:0001", Kind: "cursor-alert", Severity: event.SevEscalate,
		}},
		{"park-missing-id", event.Event{
			Source: "gate", ID: "", Kind: "escalation", Severity: event.SevEscalate,
			Fields: map[string]string{"repo": "itsHabib/workbench", "number": "9"},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := renderSlackMessage("C1", true, tc.ev)
			if btns := resolveButtons(msg.Attachments[0].Blocks); len(btns) != 0 {
				t.Fatalf("%s must carry no resolve buttons, got %+v", tc.name, btns)
			}
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
