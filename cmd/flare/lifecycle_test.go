package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
	"github.com/itsHabib/workbench/cmd/flare/internal/journal"
	"github.com/itsHabib/workbench/cmd/flare/internal/notify"
	"github.com/itsHabib/workbench/cmd/flare/internal/route"
)

// fakeCourier records what the cycle asked Slack to do, in order. The
// assertions below are about ORCHESTRATION — which card is posted, which is
// corrected, and when — so the seam is the courier, not HTTP. The rendered
// Slack payloads are pinned in internal/notify's own tests.
type fakeCourier struct {
	posts     []event.Event
	refs      map[string]notify.Ref
	finalized []finalCall
	ts        int
	sendErr   error
	finalErr  error
}

type finalCall struct {
	ref notify.Ref
	ev  event.Event
}

func (f *fakeCourier) courier() courier {
	return courier{
		send: func(_ config.Channel, ev event.Event) (notify.Ref, error) {
			if f.sendErr != nil {
				return notify.Ref{}, f.sendErr
			}
			f.ts++
			ref := notify.Ref{ChannelID: "C999", TS: tsFor(f.ts)}
			f.posts = append(f.posts, ev)
			if f.refs == nil {
				f.refs = map[string]notify.Ref{}
			}
			f.refs[ev.ID] = ref
			return ref, nil
		},
		finalize: func(_ config.Channel, ref notify.Ref, ev event.Event) error {
			if f.finalErr != nil {
				return f.finalErr
			}
			f.finalized = append(f.finalized, finalCall{ref: ref, ev: ev})
			return nil
		},
	}
}

func tsFor(n int) string { return "170000000." + strconv.Itoa(n) }

// posted reports whether an artifact id was paged as its own card.
func (f *fakeCourier) posted(id string) bool {
	_, ok := f.refs[id]
	return ok
}

// pages counts the card-class events posted — every notification the operator
// actually received.
func (f *fakeCourier) pages() int { return len(f.posts) }

// slackGate wires a gate-log source to a slack channel with resolve actions on
// — the production shape of the phone rung — with the courier faked out.
func slackGate(t *testing.T, log string) (config.Config, *journal.Journal, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "log.jsonl")
	if err := os.WriteFile(path, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := journal.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version: config.Version,
		Sources: []config.Source{{Name: "gate", Kind: config.SourceGateLog, Path: path}},
		Channels: map[string]config.Channel{
			"phone": {Type: config.ChannelSlack, Token: "xoxb-test", ChannelID: "C999", ResolveActions: true},
		},
		CatchAll: "phone",
	}
	return cfg, j, path
}

const (
	lcGrant = `{"id":"grt_1","kind":"grant","run":"run_mint","time":"2026-08-21T20:00:00Z","body":{"repo":"itsHabib/ivy","action":"merge","max_tier":"T3","max_cycles":3,"expires_at":"2126-01-01T00:00:00Z","minted_by":"operator"}}`
	lcVrd   = `{"id":"vrd_1","kind":"verdict","run":"run_1","time":"2026-08-21T21:00:00Z","body":{"subject":{"repo":"itsHabib/ivy","number":23,"head_sha":"b33c512"},"source":"reducer","decision":"escalate","tier":"T1","why":"readiness"}}`
	lcPark  = `{"id":"esc_1","kind":"escalation","run":"run_1","time":"2026-08-21T21:00:01Z","body":{"outcome":"parked_for_judgment","verdict":"vrd_1","grant":"grt_1","question":"your call","repo":"itsHabib/ivy","number":23}}`
	lcJudge = `{"id":"jdg_1","kind":"judgment","run":"run_1","time":"2026-08-21T22:00:00Z","parents":["esc_1","grt_1"],"body":{"subject":{"repo":"itsHabib/ivy","number":23},"source":"operator-judgment","producer":{"class":"judgment","impl":"operator"},"decision":"pass","tier":"T0","why":"approved at the keyboard"}}`
	lcRes   = `{"id":"res_1","kind":"resolution","run":"run_1","time":"2026-08-21T22:00:01Z","parents":["esc_1","jdg_1"],"body":{"decision":"pass","who":"@mhdevstuff (U0B17RNEFCK)","at":"2026-08-21T22:00:01Z","judgment_id":"jdg_1"}}`
	lcVrd2  = `{"id":"vrd_2","kind":"verdict","run":"run_1","time":"2026-08-21T22:00:02Z","body":{"subject":{"repo":"itsHabib/ivy","number":23,"head_sha":"b33c512"},"source":"reducer","decision":"pass","tier":"T1","why":"judged"}}`
	lcAct   = `{"id":"act_1","kind":"action","run":"run_1","time":"2026-08-21T22:00:03Z","parents":["vrd_2"],"body":{"argv":["gh","pr","merge","23","-R","itsHabib/ivy","--squash","--match-head-commit","b33c512cfede769b128e9148b752cf6dd5836115"],"outcome":"would_merge","grant":"grt_1","verdict":"vrd_2"}}`
	// A second run parking the SAME PR — what makes the first card stale.
	lcVrd3  = `{"id":"vrd_3","kind":"verdict","run":"run_2","time":"2026-08-21T23:00:00Z","body":{"subject":{"repo":"itsHabib/ivy","number":23,"head_sha":"cafe123"},"source":"reducer","decision":"escalate","tier":"T1","why":"readiness"}}`
	lcPark2 = `{"id":"esc_2","kind":"escalation","run":"run_2","time":"2026-08-21T23:00:01Z","body":{"outcome":"parked_for_judgment","verdict":"vrd_3","grant":"grt_1","question":"your call","repo":"itsHabib/ivy","number":23}}`
)

func runCycle(t *testing.T, cfg config.Config, j *journal.Journal, f *fakeCourier) {
	t.Helper()
	if err := cycle(runner{cfg: cfg, j: j, r: route.New(cfg, time.Now), co: f.courier()}, true); err != nil {
		t.Fatalf("cycle: %v", err)
	}
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, l := range lines {
		if _, err := f.WriteString(l + "\n"); err != nil {
			t.Fatal(err)
		}
	}
}

// TestResolvedParkClosesItsCard is the fix for the night two taps died. A park
// is paged with live Approve/Block buttons, then resolved at the KEYBOARD. The
// card must be corrected to its terminal state — otherwise its buttons stay
// live in Slack and the next tap fails with "escalation is not currently
// parked: esc_… not in gate inbox", which reads as a broken tool.
func TestResolvedParkClosesItsCard(t *testing.T) {
	f := &fakeCourier{}
	cfg, j, path := slackGate(t, lcGrant+"\n"+lcVrd+"\n"+lcPark+"\n")
	runCycle(t, cfg, j, f)
	if !f.posted("esc_1") {
		t.Fatalf("the park must be paged, posts = %+v", f.posts)
	}
	if len(f.finalized) != 0 {
		t.Fatalf("nothing is resolved yet, finalized = %+v", f.finalized)
	}
	pagesAfterPark := f.pages()

	appendLines(t, path, lcJudge, lcRes)
	runCycle(t, cfg, j, f)

	if len(f.finalized) == 0 {
		t.Fatal("a resolved park must have its card corrected")
	}
	last := f.finalized[len(f.finalized)-1]
	if last.ref != f.refs["esc_1"] {
		t.Errorf("the correction must address the park's own message, got %+v want %+v", last.ref, f.refs["esc_1"])
	}
	if got := last.ev.Fields["outcome"]; got != event.OutcomeApproved {
		t.Errorf("outcome = %q, want %q", got, event.OutcomeApproved)
	}
	if got := last.ev.Fields["who"]; !strings.Contains(got, "mhdevstuff") {
		t.Errorf("who = %q, want the identity the resolution recorded", got)
	}
	if f.pages() != pagesAfterPark {
		t.Errorf("correcting a card must not page again: %d pages, was %d", f.pages(), pagesAfterPark)
	}
}

// TestMergeAuthorizationCarriesTheSHA pins the second half of the lifecycle:
// once the run reaches an authorization, the card carries the exact head gate
// pinned the merge to.
func TestMergeAuthorizationCarriesTheSHA(t *testing.T) {
	f := &fakeCourier{}
	cfg, j, path := slackGate(t, lcGrant+"\n"+lcVrd+"\n"+lcPark+"\n")
	runCycle(t, cfg, j, f)
	appendLines(t, path, lcJudge, lcRes, lcVrd2, lcAct)
	runCycle(t, cfg, j, f)

	var sawSHA bool
	for _, c := range f.finalized {
		if c.ev.Fields["sha"] == "b33c512cfede769b128e9148b752cf6dd5836115" {
			sawSHA = true
		}
	}
	if !sawSHA {
		t.Fatalf("the authorized head sha must reach the card, finalized = %+v", f.finalized)
	}
}

// TestNewParkSupersedesTheOlderCard is the stale-card failure mode itself.
// gate's inbox keeps only the latest terminal per PR, so the instant a fresh
// run parks the same PR the older card's Approve resolves nothing. The older
// card must be closed BEFORE the newer one is posted — otherwise there is a
// window with two live Approve buttons for one PR, only one of which works.
func TestNewParkSupersedesTheOlderCard(t *testing.T) {
	f := &fakeCourier{}
	cfg, j, path := slackGate(t, lcGrant+"\n"+lcVrd+"\n"+lcPark+"\n")
	runCycle(t, cfg, j, f)
	appendLines(t, path, lcVrd3, lcPark2)
	runCycle(t, cfg, j, f)

	if len(f.finalized) != 1 {
		t.Fatalf("exactly the older card must be closed, finalized = %+v", f.finalized)
	}
	closed := f.finalized[0]
	if closed.ref != f.refs["esc_1"] {
		t.Errorf("the OLDER card must be the one closed, got %+v want %+v", closed.ref, f.refs["esc_1"])
	}
	if got := closed.ev.Fields["outcome"]; got != event.OutcomeSuperseded {
		t.Errorf("outcome = %q, want %q", got, event.OutcomeSuperseded)
	}
	// The newer park still pages: supersession corrects the old card, it never
	// suppresses the new one.
	if !f.posted("esc_2") {
		t.Fatalf("the newer park must still be paged, posts = %+v", f.posts)
	}
}

// TestCardUpdatesAreNotPages pins the class boundary: a terminal artifact must
// never itself become a notification. gate's live ledger holds 237 judgments;
// routing them would page the operator for every one.
func TestCardUpdatesAreNotPages(t *testing.T) {
	f := &fakeCourier{}
	cfg, j, _ := slackGate(t, lcGrant+"\n"+lcVrd+"\n"+lcJudge+"\n"+lcRes+"\n"+lcVrd2+"\n"+lcAct+"\n")
	runCycle(t, cfg, j, f)
	for _, ev := range f.posts {
		if ev.Class == event.ClassUpdate {
			t.Fatalf("a terminal artifact must never be routed as a page, got %+v", ev)
		}
	}
	for _, id := range []string{"jdg_1", "res_1", "act_1"} {
		if f.posted(id) {
			t.Fatalf("terminal artifact %s must not become its own card", id)
		}
	}
	if len(f.finalized) != 0 {
		t.Fatalf("with no live card there is nothing to correct, finalized = %+v", f.finalized)
	}
	// Settled, not pending: a later cycle must not retry them forever.
	replayed, err := j.Load(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	seen := replayed.Seen
	for _, id := range []string{"jdg_1", "res_1", "act_1"} {
		if !seen[journal.SeenKey("gate", id)] {
			t.Errorf("update %s must settle even with no card to apply it to", id)
		}
	}
}

// TestClosedCardIsNotClosedTwice pins idempotence across restarts: the card
// index is replayed from the journal, so a card already corrected must not be
// corrected again by a later terminal artifact for the same PR.
func TestClosedCardIsNotClosedTwice(t *testing.T) {
	f := &fakeCourier{}
	cfg, j, path := slackGate(t, lcGrant+"\n"+lcVrd+"\n"+lcPark+"\n")
	runCycle(t, cfg, j, f)
	appendLines(t, path, lcJudge, lcRes)
	runCycle(t, cfg, j, f)
	before := len(f.finalized)
	appendLines(t, path, lcVrd2, lcAct)
	runCycle(t, cfg, j, f)
	if after := len(f.finalized); after != before {
		t.Fatalf("a closed card must not be corrected again: %d before, %d after", before, after)
	}
}

// TestCardIndexSurvivesRestart pins the durability the journal exists for: a
// fresh Journal handle over the same directory must still know which cards are
// standing in Slack, or a restart would strand every live card forever.
func TestCardIndexSurvivesRestart(t *testing.T) {
	f := &fakeCourier{}
	cfg, j, path := slackGate(t, lcGrant+"\n"+lcVrd+"\n"+lcPark+"\n")
	runCycle(t, cfg, j, f)

	// slackGate puts the log and the state dir side by side under one tempdir.
	restarted, err := journal.Open(filepath.Join(filepath.Dir(path), "state"))
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := restarted.Load(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cards := replayed.Cards
	card, ok := cards[journal.SeenKey("gate", "esc_1")]
	if !ok {
		t.Fatalf("the delivered card must be replayable, got %+v", cards)
	}
	if card.Subject != "itsHabib/ivy#23" || card.TS != f.refs["esc_1"].TS {
		t.Errorf("replayed card = %+v, want the subject and ts it was posted with", card)
	}

	appendLines(t, path, lcJudge, lcRes)
	runCycle(t, cfg, restarted, f)
	if len(f.finalized) != 1 {
		t.Fatalf("the restarted flare must still correct the card, finalized = %+v", f.finalized)
	}
}

// TestFailedCorrectionRetries pins the retry contract at the new seam: a
// correction that could not be delivered must hold the cursor so the next cycle
// tries again — and must SURFACE, because the cursor is ordered and everything
// behind the stuck event is blocked with it. A card left showing a dead Approve
// button is exactly the failure this work exists to remove, so it must never be
// silently abandoned.
func TestFailedCorrectionRetries(t *testing.T) {
	f := &fakeCourier{}
	cfg, j, path := slackGate(t, lcGrant+"\n"+lcVrd+"\n"+lcPark+"\n")
	runCycle(t, cfg, j, f)

	appendLines(t, path, lcJudge, lcRes)
	f.finalErr = errors.New("slack is down")
	err := cycle(runner{cfg: cfg, j: j, r: route.New(cfg, time.Now), co: f.courier()}, true)
	if err == nil {
		t.Fatal("a stalled source must surface: sweep exits non-zero and status reports unhealthy")
	}
	if len(f.finalized) != 0 {
		t.Fatalf("nothing was delivered, finalized = %+v", f.finalized)
	}
	cur, cerr := j.LoadCursors()
	if cerr != nil {
		t.Fatal(cerr)
	}
	if _, stalled := cur.Stalled["gate"]; !stalled {
		t.Fatalf("the stall must be recorded where status reads it, got %+v", cur.Stalled)
	}

	f.finalErr = nil
	runCycle(t, cfg, j, f)
	if len(f.finalized) == 0 {
		t.Fatal("the correction must be retried once delivery recovers")
	}
	cur, cerr = j.LoadCursors()
	if cerr != nil {
		t.Fatal(cerr)
	}
	if len(cur.Stalled) != 0 {
		t.Fatalf("a recovered source must clear its stall, got %+v", cur.Stalled)
	}
}
