package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/itsHabib/workbench/cmd/flare/internal/config"
	"github.com/itsHabib/workbench/cmd/flare/internal/event"
	"github.com/itsHabib/workbench/cmd/flare/internal/journal"
	"github.com/itsHabib/workbench/cmd/flare/internal/preflight"
	"github.com/itsHabib/workbench/cmd/flare/internal/route"
	"github.com/itsHabib/workbench/cmd/flare/internal/source"
)

// The three shapes gate persists for a refusal. The first two are the bare
// top-level capability refusal; the third is the pre-flight cycle ceiling
// (#242), which runs under a run and carries its budget.
const (
	gnAbsent  = `{"id":"gnd_a","kind":"grant_needed","run":"run_x","time":"2026-08-22T10:00:00Z","body":{"repo":"itsHabib/roxiq","reason":"grant_absent","at":"2026-08-22T10:00:00Z"}}`
	gnExpired = `{"id":"gnd_e","kind":"grant_needed","run":"run_y","time":"2026-08-22T10:01:00Z","body":{"repo":"itsHabib/ivy","reason":"grant_expired","at":"2026-08-22T10:01:00Z"}}`
	gnCycles  = `{"id":"gnd_c","kind":"grant_needed","run":"run_z","time":"2026-08-22T10:02:00Z","parents":["grt_1"],"body":{"repo":"itsHabib/ivy","number":23,"reason":"grant_cycle_exceeded","grant":"grt_1","cycles_used":3,"cycles_max":3,"at":"2026-08-22T10:02:00Z","why":"cycle 4 would exceed grant ceiling 3"}}`
)

func postedByID(f *fakeCourier, id string) (event.Event, bool) {
	for _, ev := range f.posts {
		if ev.ID == id {
			return ev, true
		}
	}
	return event.Event{}, false
}

// TestGrantNeededReachesThePhone is the whole point of the class: gate records
// 18 of these in the live ledger and flare dropped every one. Each is a run
// that stopped dead on the ONE thing no agent can do for itself.
func TestGrantNeededReachesThePhone(t *testing.T) {
	f := &fakeCourier{}
	cfg, j, _ := slackGate(t, lcGrant+"\n"+gnAbsent+"\n"+gnExpired+"\n"+gnCycles+"\n")
	runCycle(t, cfg, j, f)

	for _, id := range []string{"gnd_a", "gnd_e", "gnd_c"} {
		ev, ok := postedByID(f, id)
		if !ok {
			t.Fatalf("refusal %s must page, posts = %+v", id, f.posts)
		}
		if ev.Kind != "grant-needed" {
			t.Errorf("%s kind = %q, want grant-needed", id, ev.Kind)
		}
		if ev.Severity != event.SevEscalate {
			t.Errorf("%s severity = %v, want escalate — the repo is stopped", id, ev.Severity)
		}
		if id == "gnd_c" {
			continue // a spent cycle budget pages, but proposes no mint — see the next test
		}
		if !strings.HasPrefix(ev.Fields["mint"], "gate grant -repo ") {
			t.Errorf("%s must carry a paste-ready mint, got %q", id, ev.Fields["mint"])
		}
		if !strings.Contains(ev.Fields["mint"], "-state ") {
			t.Errorf("%s mint must pin the watched state dir, got %q", id, ev.Fields["mint"])
		}
	}
}

// TestGrantNeededMintProposesWhatWorkedBefore pins the remedy: a lapsed grant
// is re-proposed at the ceiling the repo already held, and a spent cycle
// budget proposes NOTHING — the ceiling is the stop signal that the review
// loop ran long, and a card that pastes "-max-cycles used+1" turns the stop
// signal into instructions to keep going. flare proposes what the operator has
// already judged appropriate; it never widens on its own, and it never mints.
func TestGrantNeededMintProposesWhatWorkedBefore(t *testing.T) {
	f := &fakeCourier{}
	cfg, j, _ := slackGate(t, lcGrant+"\n"+gnExpired+"\n"+gnCycles+"\n")
	runCycle(t, cfg, j, f)

	expired, _ := postedByID(f, "gnd_e")
	if !strings.Contains(expired.Fields["mint"], "-max-tier T3") {
		t.Errorf("a lapsed grant re-mints at the ceiling the repo held, got %q", expired.Fields["mint"])
	}
	cycles, _ := postedByID(f, "gnd_c")
	if cycles.Fields["mint"] != "" {
		t.Errorf("a spent cycle budget must not propose a wider grant, got %q", cycles.Fields["mint"])
	}
	if !strings.Contains(cycles.Body, "fewer rounds") {
		t.Errorf("the card must explain the exhausted budget as the stop signal, got %q", cycles.Body)
	}
	if cycles.Fields["cycles_used"] != "3" || cycles.Fields["cycles_max"] != "3" {
		t.Errorf("the budget must reach the card, got %s of %s", cycles.Fields["cycles_used"], cycles.Fields["cycles_max"])
	}
}

// parkWithReason builds a park whose question is byte-identical to the others,
// which is how 318 of 355 real parks arrive.
func parkWithReason(id, run string, number int, question string) string {
	return `{"id":"` + id + `","kind":"escalation","run":"` + run + `","time":"2026-08-22T10:00:00Z","parents":["vrd_1","grt_1"],` +
		`"body":{"outcome":"parked_for_judgment","verdict":"vrd_1","grant":"grt_1","question":"` + question + `","repo":"itsHabib/ivy","number":` +
		itoa(number) + `}}`
}

func itoa(n int) string { return strconv.Itoa(n) }

const identicalQuestion = "readiness: no review decision reported by GitHub — cannot verify readiness"

// TestIdenticalReasonsCollapse pins the habituation fix. The first two
// identical questions arrive in full; from the third on the card leads with
// what is DIFFERENT and references the boilerplate instead of restating it.
func TestIdenticalReasonsCollapse(t *testing.T) {
	f := &fakeCourier{}
	log := lcGrant + "\n" + lcVrd + "\n"
	for i, id := range []string{"esc_a", "esc_b", "esc_c", "esc_d"} {
		log += parkWithReason(id, "run_"+id, 30+i, identicalQuestion) + "\n"
	}
	cfg, j, _ := slackGate(t, log)
	runCycle(t, cfg, j, f)

	var collapsed []string
	for _, id := range []string{"esc_a", "esc_b", "esc_c", "esc_d"} {
		ev, ok := postedByID(f, id)
		if !ok {
			t.Fatalf("%s must still page — collapsing changes the CARD, never whether it is sent", id)
		}
		if ev.Fields["repeat"] != "" {
			collapsed = append(collapsed, id)
		}
	}
	if len(collapsed) != 2 || collapsed[0] != "esc_c" {
		t.Fatalf("the first two identical questions arrive in full and the rest collapse, collapsed = %v", collapsed)
	}
}

// TestDifferentReasonsDoNotCollapse keeps the collapse honest: a genuinely new
// question is never hidden behind a repeat count, and neither is the same
// sentence about a different repo.
func TestDifferentReasonsDoNotCollapse(t *testing.T) {
	f := &fakeCourier{}
	log := lcGrant + "\n" + lcVrd + "\n"
	for i, id := range []string{"esc_a", "esc_b", "esc_c"} {
		log += parkWithReason(id, "run_"+id, 30+i, identicalQuestion) + "\n"
	}
	log += parkWithReason("esc_new", "run_new", 40, "review-consolidation: a bot posted a finding on this head") + "\n"
	cfg, j, _ := slackGate(t, log)
	runCycle(t, cfg, j, f)

	fresh, ok := postedByID(f, "esc_new")
	if !ok {
		t.Fatal("a new question must page")
	}
	if fresh.Fields["repeat"] != "" {
		t.Errorf("a different reason must never be collapsed, got repeat=%q", fresh.Fields["repeat"])
	}
}

// TestStalledSourceIsUnhealthy is the honest-health contract. A fresh last_poll
// proves the loop is RUNNING; it does not prove anything is getting through.
// The cursor is ordered, so one undeliverable event blocks every event behind
// it — and that used to report healthy:true.
func TestStalledSourceIsUnhealthy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.jsonl")
	if err := os.WriteFile(path, []byte(lcGrant+"\n"+lcVrd+"\n"+lcPark+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := journal.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version:     config.Version,
		PollSeconds: 60,
		Sources:     []config.Source{{Name: "gate", Kind: config.SourceGateLog, Path: path}},
		Channels:    map[string]config.Channel{"phone": {Type: config.ChannelSlack, Token: "x", ChannelID: "C1"}},
		CatchAll:    "phone",
	}
	f := &fakeCourier{sendErr: errSendFailed}
	if err := cycle(runnerFor(cfg, j, f), true); err == nil {
		t.Fatal("a source that cannot deliver must not report a clean poll")
	}
	if code := statusCode(t, cfg, j); code != 1 {
		t.Fatalf("flare status = %d, want 1 while a source is stalled", code)
	}

	f.sendErr = nil
	if err := cycle(runnerFor(cfg, j, f), true); err != nil {
		t.Fatalf("recovered cycle: %v", err)
	}
	if code := statusCode(t, cfg, j); code != 0 {
		t.Fatalf("flare status = %d, want 0 once delivery recovers", code)
	}
}

// TestUnplaceableSourceIsUnhealthy covers the other way a source never gets
// read: it has no cursor yet and cannot be placed at its tail because the log
// is corrupt. That failure used to be printed and forgotten, so LastPoll kept
// refreshing and `flare status` reported healthy over a source flare had never
// once read.
func TestUnplaceableSourceIsUnhealthy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.jsonl")
	if err := os.WriteFile(path, []byte(lcGrant+"\n{not json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	j, err := journal.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version:     config.Version,
		PollSeconds: 60,
		Sources:     []config.Source{{Name: "gate", Kind: config.SourceGateLog, Path: path}},
		Channels:    map[string]config.Channel{"phone": {Type: config.ChannelSlack, Token: "x", ChannelID: "C1"}},
		CatchAll:    "phone",
	}
	if err := cycle(runnerFor(cfg, j, &fakeCourier{}), false); err == nil {
		t.Fatal("a source that cannot be placed must not report a clean poll")
	}
	if code := statusCode(t, cfg, j); code != 1 {
		t.Fatalf("flare status = %d, want 1 while a source has never been placed", code)
	}
}

// TestAuthorityDigestNamesTheBottleneck pins the digest's content: a repo with
// parked work and NO live grant is a hard stop and must lead, each row carries
// the exact mint, and a repo that is running fine is left out — a digest that
// lists everything is another wall of text to skim.
func TestAuthorityDigestNamesTheBottleneck(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	rows := []authorityRow{
		{Authority: source.Authority{Repo: "itsHabib/roxiq", Parked: 2, ProposedTier: "T3", ProposedCycles: 3}, state: "/state"},
		{Authority: source.Authority{Repo: "itsHabib/ivy", Parked: 1, Live: true,
			Grant: grantExpiring(now.Add(2 * time.Hour))}, state: "/state"},
		{Authority: source.Authority{Repo: "itsHabib/rooms", Parked: 3, Live: true,
			Grant: grantExpiring(now.Add(48 * time.Hour))}, state: "/state"},
		{Authority: source.Authority{Repo: "itsHabib/quiet", Parked: 0}, state: "/state"},
	}
	ev, ok := digestEvent(rows, now, 12*time.Hour)
	if !ok {
		t.Fatal("a repo with parked work and no grant must produce a digest")
	}
	detail := ev.Fields["detail"]
	if !strings.Contains(detail, "itsHabib/roxiq") || !strings.Contains(detail, "no live grant") {
		t.Errorf("the hard stop must be named:\n%s", detail)
	}
	if !strings.Contains(detail, "itsHabib/ivy") || !strings.Contains(detail, "expires in 2h") {
		t.Errorf("a grant lapsing under parked work must be called out:\n%s", detail)
	}
	if strings.Contains(detail, "itsHabib/rooms") {
		t.Errorf("a repo with a healthy grant must be left out:\n%s", detail)
	}
	if strings.Contains(detail, "itsHabib/quiet") {
		t.Errorf("a repo with nothing parked is not pressure:\n%s", detail)
	}
	if strings.Count(detail, "gate grant -repo") != 2 {
		t.Errorf("every row needs its own paste-ready mint:\n%s", detail)
	}
	// A repo with no LIVE grant still proposes the ceilings it has held before,
	// exactly as a single refusal card does — the two surfaces must not give
	// different advice about the same repo.
	if !strings.Contains(detail, "-repo itsHabib/roxiq -max-tier T3") {
		t.Errorf("a lapsed repo must be re-proposed at the ceiling it held:\n%s", detail)
	}
	if ev.Severity != event.SevEscalate {
		t.Errorf("severity = %v, want escalate while something is hard-stopped", ev.Severity)
	}
}

// TestAuthorityDigestIsSilentWhenNothingNeedsYou keeps the digest from becoming
// the next thing the operator learns to ignore.
func TestAuthorityDigestIsSilentWhenNothingNeedsYou(t *testing.T) {
	now := time.Now()
	rows := []authorityRow{
		{Authority: source.Authority{Repo: "itsHabib/ivy", Parked: 2, Live: true, Grant: grantExpiring(now.Add(48 * time.Hour))}},
		{Authority: source.Authority{Repo: "itsHabib/roxiq", Parked: 0}},
	}
	if _, ok := digestEvent(rows, now, 12*time.Hour); ok {
		t.Fatal("no pressure must produce no digest")
	}
}

// TestAuthorityDigestIDTracksContent pins the dedupe: an unchanged picture is
// not news and must not re-page, while any movement in it must.
func TestAuthorityDigestIDTracksContent(t *testing.T) {
	now := time.Now()
	rows := []authorityRow{{Authority: source.Authority{Repo: "itsHabib/roxiq", Parked: 2}}}
	first, _ := digestEvent(rows, now, 12*time.Hour)
	same, _ := digestEvent(rows, now.Add(time.Hour), 12*time.Hour)
	if first.ID != same.ID {
		t.Errorf("an unchanged digest must keep its id: %s vs %s", first.ID, same.ID)
	}
	rows[0].Parked = 3
	moved, _ := digestEvent(rows, now, 12*time.Hour)
	if moved.ID == first.ID {
		t.Error("a digest whose content moved must page again")
	}

	// An expiring grant renders a countdown that moves every minute; the id
	// must hash the absolute expiry, not the rendered text, or every scheduled
	// run pages the same picture.
	expiring := []authorityRow{{Authority: source.Authority{Repo: "itsHabib/ivy", Parked: 1, Live: true,
		Grant: grantExpiring(now.Add(2 * time.Hour))}}}
	a, _ := digestEvent(expiring, now, 12*time.Hour)
	b, _ := digestEvent(expiring, now.Add(7*time.Minute), 12*time.Hour)
	if a.Fields["detail"] == b.Fields["detail"] {
		t.Fatal("test premise: the countdown must have moved")
	}
	if a.ID != b.ID {
		t.Errorf("a ticking countdown is not news: %s vs %s", a.ID, b.ID)
	}
}

func runnerFor(cfg config.Config, j *journal.Journal, f *fakeCourier) runner {
	return runner{cfg: cfg, j: j, r: route.New(cfg, time.Now), co: f.courier()}
}

func grantExpiring(at time.Time) preflight.Grant {
	return preflight.Grant{ID: "grt_1", Repo: "r", MaxTier: "T2", MaxCycles: 3, ExpiresAt: at}
}

// statusCode runs the status verb and returns its exit code, with stdout
// discarded — the code is the contract /health reads.
func statusCode(t *testing.T, cfg config.Config, j *journal.Journal) int {
	t.Helper()
	stdout := os.Stdout
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = devnull
	defer func() {
		os.Stdout = stdout
		devnull.Close()
	}()
	return status(cfg, j)
}

var errSendFailed = errSend{}

type errSend struct{}

func (errSend) Error() string { return "slack is down" }
