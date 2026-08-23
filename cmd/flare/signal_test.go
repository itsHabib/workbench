package main

import (
	"os"
	"path/filepath"
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
		if !strings.HasPrefix(ev.Fields["mint"], "gate grant -repo ") {
			t.Errorf("%s must carry a paste-ready mint, got %q", id, ev.Fields["mint"])
		}
		if !strings.Contains(ev.Fields["mint"], "-state ") {
			t.Errorf("%s mint must pin the watched state dir, got %q", id, ev.Fields["mint"])
		}
	}
}

// TestGrantNeededMintProposesWhatWorkedBefore pins the remedy's arithmetic: a
// spent cycle budget needs a WIDER -max-cycles at the same tier, and a lapsed
// grant is re-proposed at the ceiling the repo already held. flare proposes
// what the operator has already judged appropriate; it never widens on its own,
// and it never mints.
func TestGrantNeededMintProposesWhatWorkedBefore(t *testing.T) {
	f := &fakeCourier{}
	cfg, j, _ := slackGate(t, lcGrant+"\n"+gnExpired+"\n"+gnCycles+"\n")
	runCycle(t, cfg, j, f)

	expired, _ := postedByID(f, "gnd_e")
	if !strings.Contains(expired.Fields["mint"], "-max-tier T3") {
		t.Errorf("a lapsed grant re-mints at the ceiling the repo held, got %q", expired.Fields["mint"])
	}
	cycles, _ := postedByID(f, "gnd_c")
	if !strings.Contains(cycles.Fields["mint"], "-max-cycles 4") {
		t.Errorf("a spent budget needs one more cycle than consumed, got %q", cycles.Fields["mint"])
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

func itoa(n int) string { return string(rune('0'+n/10)) + string(rune('0'+n%10)) }

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

// TestAuthorityDigestNamesTheBottleneck pins the digest's content: a repo with
// parked work and NO live grant is a hard stop and must lead, each row carries
// the exact mint, and a repo that is running fine is left out — a digest that
// lists everything is another wall of text to skim.
func TestAuthorityDigestNamesTheBottleneck(t *testing.T) {
	now := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)
	rows := []authorityRow{
		{Authority: source.Authority{Repo: "itsHabib/roxiq", Parked: 2}, state: "/state"},
		{Authority: source.Authority{Repo: "itsHabib/ivy", Parked: 1, Live: true,
			Grant: grantExpiring(now.Add(2 * time.Hour))}, state: "/state"},
		{Authority: source.Authority{Repo: "itsHabib/rooms", Parked: 3, Live: true,
			Grant: grantExpiring(now.Add(48 * time.Hour))}, state: "/state"},
		{Authority: source.Authority{Repo: "itsHabib/quiet", Parked: 0}, state: "/state"},
	}
	ev, ok := digestEvent(config.Config{}, rows, now, 12*time.Hour)
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
	if _, ok := digestEvent(config.Config{}, rows, now, 12*time.Hour); ok {
		t.Fatal("no pressure must produce no digest")
	}
}

// TestAuthorityDigestIDTracksContent pins the dedupe: an unchanged picture is
// not news and must not re-page, while any movement in it must.
func TestAuthorityDigestIDTracksContent(t *testing.T) {
	now := time.Now()
	rows := []authorityRow{{Authority: source.Authority{Repo: "itsHabib/roxiq", Parked: 2}}}
	first, _ := digestEvent(config.Config{}, rows, now, 12*time.Hour)
	same, _ := digestEvent(config.Config{}, rows, now.Add(time.Hour), 12*time.Hour)
	if first.ID != same.ID {
		t.Errorf("an unchanged digest must keep its id: %s vs %s", first.ID, same.ID)
	}
	rows[0].Parked = 3
	moved, _ := digestEvent(config.Config{}, rows, now, 12*time.Hour)
	if moved.ID == first.ID {
		t.Error("a digest whose content moved must page again")
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
