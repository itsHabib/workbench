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
	"github.com/itsHabib/workbench/cmd/flare/internal/route"
	"github.com/itsHabib/workbench/cmd/flare/internal/source"
)

const (
	firstRunEsc = `{"id":"esc_old","kind":"escalation","run":"run_old","time":"2026-07-08T16:37:12Z","body":{"outcome":"parked_for_judgment","question":"long dead"},"prev":"","hash":"h1"}`
	firstRunVrd = `{"id":"vrd_old","kind":"verdict","run":"run_old","time":"2026-07-08T16:37:13Z","body":{"subject":{"repo":"itsHabib/ship","number":1},"source":"reducer","decision":"block","tier":"T1","why":"old"},"prev":"h1","hash":"h2"}`
	firstRunNew = `{"id":"esc_new","kind":"escalation","run":"run_new","time":"2026-08-21T10:00:00Z","body":{"outcome":"parked_for_judgment","question":"live"},"prev":"h2","hash":"h3"}`
)

// quietGate is a gate-log source over a seeded log plus a config whose
// catch-all is a drop channel, so every routed event lands in the journal as
// dropped — the observable "was this delivered to routing" fact — without any
// transport. Returns the config, the journal, and the log path.
func quietGate(t *testing.T, log string) (config.Config, *journal.Journal, string) {
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
		Version:  config.Version,
		Sources:  []config.Source{{Name: "gate", Kind: config.SourceGateLog, Path: path}},
		Channels: map[string]config.Channel{"quiet": {Type: config.ChannelDrop}},
		CatchAll: "quiet",
	}
	return cfg, j, path
}

// routedIDs returns the event IDs the journal saw reach routing (dropped here),
// in order, plus every cursor-init note — the two facts the first-run contract
// is made of.
func routedIDs(t *testing.T, j *journal.Journal) ([]string, []string) {
	t.Helper()
	tail, err := j.Tail(100)
	if err != nil {
		t.Fatal(err)
	}
	var ids, inits []string
	for _, e := range tail {
		if e.Kind == journal.CursorInit {
			inits = append(inits, e.Note)
			continue
		}
		ids = append(ids, e.EventID)
	}
	return ids, inits
}

func TestFirstRunStartsAtTailAndDeliversOnlyWhatFollows(t *testing.T) {
	// Fresh state (no cursors.json at all) must NOT replay the log: a 4k-line
	// gate ledger paged 63 dead escalations into Slack before 429 on the first
	// run of a new machine. The cursor is placed at the tail, the placement is
	// journaled, and only what is appended afterwards is delivered.
	cfg, j, path := quietGate(t, firstRunEsc+"\n"+firstRunVrd+"\n")
	r := route.New(cfg, time.Now)
	if err := cycle(cfg, j, r, false); err != nil {
		t.Fatal(err)
	}
	ids, inits := routedIDs(t, j)
	if len(ids) != 0 {
		t.Fatalf("first run must not deliver the backlog, routed %v", ids)
	}
	if len(inits) != 1 || !strings.Contains(inits[0], "initialized at tail") {
		t.Fatalf("first run must journal exactly one cursor-init note, got %v", inits)
	}
	cur, err := j.LoadCursors()
	if err != nil {
		t.Fatal(err)
	}
	want := source.Cursor{Offset: int64(len(firstRunEsc) + 1 + len(firstRunVrd) + 1), LastHash: "h2"}
	if got := cur.Sources["gate"]; got != want {
		t.Fatalf("cursor must sit at the tail pinned to the last hash: got %+v want %+v", got, want)
	}

	// What arrives after placement is new and must page.
	if err := os.WriteFile(path, []byte(firstRunEsc+"\n"+firstRunVrd+"\n"+firstRunNew+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cycle(cfg, j, r, false); err != nil {
		t.Fatal(err)
	}
	ids, inits = routedIDs(t, j)
	if len(ids) != 1 || ids[0] != "esc_new" {
		t.Fatalf("the appended escalation must be delivered (and nothing older), routed %v", ids)
	}
	if len(inits) != 1 {
		t.Fatalf("placement happens once; got %d cursor-init notes", len(inits))
	}
}

func TestFirstPlacementIsPersistedBeforePolling(t *testing.T) {
	// The placement must be on disk the moment it is made, not at the end of
	// the cycle: a crash (or failed end-of-cycle save) between placement and
	// save would otherwise re-place the source at a newer tail next cycle and
	// silently skip everything appended in between.
	cfg, j, _ := quietGate(t, firstRunEsc+"\n"+firstRunVrd+"\n")
	cur := journal.Cursors{Sources: map[string]source.Cursor{}}
	if err := placeCursor(j, cfg.Sources[0], cur, false); err != nil {
		t.Fatal(err)
	}
	onDisk, err := j.LoadCursors()
	if err != nil {
		t.Fatal(err)
	}
	want := source.Cursor{Offset: int64(len(firstRunEsc) + 1 + len(firstRunVrd) + 1), LastHash: "h2"}
	if got := onDisk.Sources["gate"]; got != want {
		t.Fatalf("placement must be durable before any poll: on disk %+v, want %+v", got, want)
	}
	if _, inits := routedIDs(t, j); len(inits) != 1 {
		t.Fatalf("the persisted placement must be the journaled one, got %v", inits)
	}
}

func TestFromStartDeliversTheWholeHistory(t *testing.T) {
	// The deliberate opt-in: -from-start places a never-seen source at offset 0
	// so the full history pages, and says so in the journal.
	cfg, j, _ := quietGate(t, firstRunEsc+"\n"+firstRunVrd+"\n")
	if err := cycle(cfg, j, route.New(cfg, time.Now), true); err != nil {
		t.Fatal(err)
	}
	ids, inits := routedIDs(t, j)
	if len(ids) != 2 || ids[0] != "esc_old" || ids[1] != "vrd_old" {
		t.Fatalf("-from-start must deliver the backlog in order, routed %v", ids)
	}
	if len(inits) != 1 || !strings.Contains(inits[0], "-from-start") {
		t.Fatalf("-from-start must journal its placement, got %v", inits)
	}
}

func TestResweepAfterChainBreakStillDelivers(t *testing.T) {
	// A present cursor whose pinned hash no longer matches the log is a
	// resweep, not a first run: the chain-break alert fires and the history is
	// re-lifted from offset 0 (dedupe is what keeps a resweep from re-paging,
	// and here nothing was paged before). The tail placement must never
	// swallow this path.
	cfg, j, _ := quietGate(t, firstRunEsc+"\n"+firstRunVrd+"\n")
	stale := journal.Cursors{Sources: map[string]source.Cursor{"gate": {Offset: int64(len(firstRunEsc) + 1), LastHash: "not-h1"}}}
	if err := j.SaveCursors(stale); err != nil {
		t.Fatal(err)
	}
	if err := cycle(cfg, j, route.New(cfg, time.Now), false); err != nil {
		t.Fatal(err)
	}
	ids, inits := routedIDs(t, j)
	if len(inits) != 0 {
		t.Fatalf("a reset cursor is not fresh state; no cursor-init expected, got %v", inits)
	}
	if len(ids) != 3 || !strings.HasPrefix(ids[0], "cursor-alert:") || ids[1] != "esc_old" || ids[2] != "vrd_old" {
		t.Fatalf("chain break must alert and resweep the history, routed %v", ids)
	}
}

func TestCorruptCursorRecoveryResweepsFromZeroNotTail(t *testing.T) {
	// End to end through cycle: a corrupt cursors.json must still resweep the
	// whole log (the recovery contract), never be mistaken for a first run and
	// placed at the tail.
	cfg, j, _ := quietGate(t, firstRunEsc+"\n"+firstRunVrd+"\n")
	if err := os.WriteFile(filepath.Join(filepath.Dir(cfg.Sources[0].Path), "state", "cursors.json"), []byte(`garbage`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cycle(cfg, j, route.New(cfg, time.Now), false); err != nil {
		t.Fatal(err)
	}
	ids, inits := routedIDs(t, j)
	if len(inits) != 0 {
		t.Fatalf("corrupt-cursor recovery is a resweep, not a first run, got cursor-init %v", inits)
	}
	if len(ids) != 3 || ids[0] != "cursor-alert:flare:cursors-corrupt" || ids[1] != "esc_old" || ids[2] != "vrd_old" {
		t.Fatalf("recovery must page the alert then resweep the history, routed %v", ids)
	}
}

func TestSweepFailsWhenASourceCannotBeRead(t *testing.T) {
	// The CLI contract: `flare sweep` exits non-zero on a config/source error.
	// A source whose file is missing must not read as a clean sweep (exit 0) —
	// a broken source that looks swept is the silent failure this guards.
	dir := t.TempDir()
	j, err := journal.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version:     config.Version,
		PollSeconds: 60,
		Sources:     []config.Source{{Name: "gate", Kind: config.SourceGateLog, Path: filepath.Join(dir, "missing.jsonl")}},
		Channels:    map[string]config.Channel{"toast": {Type: config.ChannelToast}},
		CatchAll:    "toast",
	}
	if code := sweep(cfg, j, false); code == 0 {
		t.Fatal("sweep must exit non-zero when a configured source cannot be read")
	}
}

func TestCorruptCursorsPagesThenQuarantinesAndResweeps(t *testing.T) {
	// A corrupt cursor file is why flare goes dark, so recovery must PAGE the
	// operator through the notify path (journaled), not silently reset — and it
	// must quarantine only after that alert settles, handing back an empty cursor
	// so the cycle resweeps.
	dir := t.TempDir()
	j, err := journal.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cursors.json"), []byte(`{"last_poll":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version:  config.Version,
		Sources:  []config.Source{{Name: "gate", Kind: config.SourceGateLog, Path: "/x"}},
		Channels: map[string]config.Channel{"quiet": {Type: config.ChannelDrop}},
		CatchAll: "quiet",
	}
	cur, err := recoverCorruptCursors(cfg, j, route.New(cfg, time.Now))
	if err != nil {
		t.Fatalf("recovery must succeed once the alert settles, got %v", err)
	}
	// The reset must be an explicit zero cursor per configured source, never an
	// absent one: absent is fresh state and would be placed at the tail,
	// silently skipping whatever the corruption hid.
	reset, ok := cur.Sources["gate"]
	if !ok || reset != (source.Cursor{}) || !cur.LastPoll.IsZero() {
		t.Fatalf("recovered cursors must hold an explicit offset-0 cursor per source so the cycle resweeps, got %+v", cur)
	}
	// The reset is durable before the resweep runs: the quarantined file is
	// gone, and a fresh cursors.json holding the zero cursors has replaced it,
	// so a crash mid-resweep restarts into a resweep — never into fresh state.
	onDisk, err := j.LoadCursors()
	if err != nil {
		t.Fatalf("reset cursors must be saved before the resweep, got %v", err)
	}
	if got, ok := onDisk.Sources["gate"]; !ok || got != (source.Cursor{}) {
		t.Fatalf("reset zero cursor must be on disk before the resweep, got %+v", onDisk)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "cursors.json.corrupt-*"))
	if len(matches) != 1 {
		t.Fatalf("the corrupt file must be quarantined aside for forensics, found %v", matches)
	}
	tail, err := j.Tail(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 || tail[0].EventID != "cursor-alert:flare:cursors-corrupt" {
		t.Fatalf("recovery must route the corruption alert through the notifier, got %+v", tail)
	}
}

func TestNamedDropChannelIsJournaledAsDropped(t *testing.T) {
	// A route to a configured channel of type drop must journal the event as
	// dropped, not delivered, even though its name is not the literal "drop".
	j, err := journal.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Version:  config.Version,
		Sources:  []config.Source{{Name: "gate", Kind: config.SourceGateLog, Path: "/x"}},
		Channels: map[string]config.Channel{"toast": {Type: config.ChannelToast}, "quiet": {Type: config.ChannelDrop}},
		Routes:   []config.Route{{Match: config.Match{Source: "gate"}, Channel: "quiet"}},
		CatchAll: "toast",
	}
	r := route.New(cfg, time.Now)
	ev := event.Event{Source: "gate", ID: "e1", Kind: "verdict", Severity: event.SevBlock, Fields: map[string]string{}}
	if ok := dispatch(cfg, j, r, ev); !ok {
		t.Fatal("dispatch to a drop channel must settle (return true)")
	}
	tail, err := j.Tail(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 1 || tail[0].Kind != journal.Dropped {
		t.Fatalf("a named drop channel must journal as dropped, got %+v", tail)
	}
}
